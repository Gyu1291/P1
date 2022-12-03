// Contains the implementation of a LSP server.

package lsp

import (
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/cmu440/lspnet"
)

type server struct {
	params *Params
	conn   *lspnet.UDPConn

	currEpoch int // current time

	closeEpoch        int // epoch when Close() was called
	timeoutCloseEpoch int // epoch of the most recent timeout close of a client

	numClient    int
	client       map[string]*ConnectionInfo
	connIdToAddr map[int]string // convert connection ID to address (string)

	queryChan chan Query

	readChan      chan *Message
	closeConnChan chan bool
	closeChan     chan int

	processingEnd chan bool
	timerEnd      chan bool
}

// NewServer creates, initiates, and returns a new server. This function should
// NOT block. Instead, it should spawn one or more goroutines (to handle things
// like accepting incoming client connections, triggering epoch events at
// fixed intervals, synchronizing events using a for-select loop like you saw in
// project 0, etc.) and immediately return. It should return a non-nil error if
// there was an error resolving or listening on the specified port number.
func NewServer(port int, params *Params) (Server, error) {
	hostport := lspnet.JoinHostPort("localhost", strconv.Itoa(port))
	laddr, err := lspnet.ResolveUDPAddr("udp", hostport)
	if err != nil {
		return nil, err
	}
	conn, err := lspnet.ListenUDP("udp", laddr)
	if err != nil {
		return nil, err
	}

	s := &server{
		params: params,
		conn:   conn,

		currEpoch: 0,

		closeEpoch:        -1,
		timeoutCloseEpoch: -1,

		numClient:    0,
		client:       make(map[string]*ConnectionInfo),
		connIdToAddr: make(map[int]string),

		queryChan: make(chan Query),

		readChan:      make(chan *Message),
		closeConnChan: make(chan bool),
		closeChan:     make(chan int),

		processingEnd: make(chan bool),
		timerEnd:      make(chan bool),
	}

	go s.ProcessQuery()
	go s.ReceivePacket()
	go s.Timer()

	return s, nil
}

func (s *server) Read() (int, []byte, error) {
	for {
		s.queryChan <- Query{Type: Read} // request read query
		msg := <-s.readChan
		if msg.ConnID != -1 {
			if msg.SeqNum == -1 {
				// client msg.ConnID disconnected
				return msg.ConnID, nil, errors.New("disconnected")
			}
			return msg.ConnID, msg.Payload, nil
		}
	}
}

func (s *server) Write(connId int, payload []byte) error {
	msg := &Message{
		Type:    MsgData,
		ConnID:  connId,
		Size:    len(payload),
		Payload: payload,
		// SeqNum, Checksum will be written in HandleWrite()
	}
	s.queryChan <- Query{Type: Write, msg: msg} // could block..???
	return nil
}

func (s *server) CloseConn(connId int) error {
	msg := &Message{ConnID: connId}
	s.queryChan <- Query{Type: CloseConn, msg: msg}
	success := <-s.closeConnChan
	if success {
		return nil
	}
	return errors.New("connection by connId doesn't exist")
}

func (s *server) Close() error {
	for {
		s.queryChan <- Query{Type: Close}
		unfinished := <-s.closeChan
		if unfinished > 0 {
			continue
		}

		s.processingEnd <- true // shutdown ProcessQuery()
		s.timerEnd <- true      // shutdown Timer()
		s.conn.Close()          // shutdown ReceivePacket()

		if unfinished < 0 {
			return errors.New("at least one connection lost while trying to close")
		}
		return nil
	}
}

func (s *server) ProcessQuery() {
	for {
		select {
		case <-s.processingEnd:
			return
		case q := <-s.queryChan:
			switch q.Type {
			case Read:
				s.HandleRead()
			case Write:
				s.HandleWrite(&q)
			case Timer:
				s.HandleTimer()
			case Receive:
				s.HandleReceive(&q)
			case CloseConn:
				s.HandleCloseConn(q.msg.ConnID)
			case Close:
				s.HandleClose()
			}
		}

	}
}

func (s *server) ReceivePacket() {
	buffer := make([]byte, 1500)
	for {
		n, addr, err := s.conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}

		msg := &Message{}
		err = json.Unmarshal(buffer[:n], msg)
		if err != nil {
			return
		}

		if len(msg.Payload) < msg.Size {
			continue
		} else if len(msg.Payload) > msg.Size {
			// truncate
			msg.Payload = msg.Payload[:msg.Size]
		}

		chksum := CalculateChecksum(msg.ConnID, msg.SeqNum, msg.Size, msg.Payload)
		if chksum != msg.Checksum {
			continue
		}

		s.queryChan <- Query{Type: Receive, addr: addr, msg: msg}
	}
}

func (s *server) Timer() {
	for {
		timer := time.NewTimer(time.Millisecond * time.Duration(s.params.EpochMillis))
		select {
		case <-s.timerEnd:
			return
		case <-timer.C:
			s.queryChan <- Query{Type: Timer}
		}
	}
}

func (s *server) HandleRead() {
	for _, cli := range s.client {
		// Check if we can read some message in order
		msg, exist := cli.recvBuf[cli.readBase]
		if exist {
			s.readChan <- msg
			delete(cli.recvBuf, cli.readBase)
			cli.readBase++
			return
		}

		// Tell Read() that client is closed
		if (cli.status == 1 && len(cli.sendBuf) == 0) || cli.status == 2 {
			cli.status = 3
			s.readChan <- &Message{Type: MsgData, ConnID: cli.id, SeqNum: -1}
			return
		}
	}
	// failed to read... make Read() request again
	s.readChan <- &Message{Type: MsgData, ConnID: -1}
}

func (s *server) HandleWrite(q *Query) {
	// Sanity check
	addr, exist := s.connIdToAddr[q.msg.ConnID]
	if !exist {
		panic("What the fuck?")
	}
	cli, exist := s.client[addr]
	if !exist {
		panic("What the fuck?")
	}

	cli.lastSeqnum++
	q.msg.SeqNum = cli.lastSeqnum
	q.msg.Checksum = CalculateChecksum(q.msg.ConnID, q.msg.SeqNum, q.msg.Size, q.msg.Payload)

	cli.sendBuf[q.msg.SeqNum] = &SendMessage{
		retransmitEpoch: -1,
		currentBackoff:  0,
		msg:             q.msg,
	}

	s.sendPending(cli)
}

func (s *server) HandleTimer() {
	s.currEpoch++

	for _, cli := range s.client {
		if cli.status >= 2 {
			continue
		}

		// close connection that exceeded epoch limit
		if s.currEpoch-cli.lastRecvEpoch >= s.params.EpochLimit {
			s.timeoutCloseEpoch = s.currEpoch
			cli.status = 2
			continue
		}

		// send heartbeat to client
		if cli.status == 0 && cli.lastSendEpoch < s.currEpoch-1 {
			s.sendAck(cli, 0)
		}

		// retransmit sent but unacked messages
		for _, send_msg := range cli.sendBuf {
			if send_msg.retransmitEpoch == -1 {
				continue // message not sent yet!
			} else if send_msg.retransmitEpoch <= s.currEpoch {
				to_send, err := json.Marshal(send_msg.msg)
				if err != nil {
					continue
				}
				_, err = s.conn.WriteToUDP(to_send, &cli.addr)
				if err != nil {
					continue
				}
				send_msg.currentBackoff = nextBackoff(send_msg.currentBackoff, s.params.MaxBackOffInterval)
				send_msg.retransmitEpoch = s.currEpoch + send_msg.currentBackoff + 1
			}
		}
	}
}

func (s *server) HandleReceive(q *Query) {
	cli, cli_exist := s.client[q.addr.String()]

	switch q.msg.Type {
	case MsgConnect:
		/* Setup new connection */
		if !cli_exist {
			s.numClient++
			cli = &ConnectionInfo{
				status: 0,

				id:   s.numClient,
				addr: *q.addr,

				lastRecvEpoch: s.currEpoch,
				lastSendEpoch: s.currEpoch,

				sendBase:   1,
				nextSeqnum: 1,
				lastSeqnum: 0,
				sendBuf:    make(map[int]*SendMessage),

				recvBase: q.msg.SeqNum + 1,
				readBase: q.msg.SeqNum + 1,
				recvBuf:  make(map[int]*Message),
			}
			s.client[q.addr.String()] = cli
			s.connIdToAddr[cli.id] = q.addr.String()
		}
		s.sendAck(cli, q.msg.SeqNum) // send connection ack

	case MsgData:
		if !cli_exist {
			panic("What the fuck")
		}

		s.sendAck(cli, q.msg.SeqNum)

		// Already received...!?
		_, msg_exist := cli.recvBuf[q.msg.SeqNum]
		if q.msg.SeqNum < cli.recvBase || msg_exist {
			break
		}

		// increment recvBase if possible
		cli.recvBuf[q.msg.SeqNum] = q.msg
		for {
			_, msg_exist := cli.recvBuf[cli.recvBase]
			if !msg_exist {
				break
			}
			cli.recvBase++
		}

	case MsgAck:
		if !cli_exist {
			panic("What the fuck?")
		}

		// Already received ACK or heartbeat?
		if q.msg.SeqNum < cli.sendBase {
			break
		}

		// Delete seqnum from sendBuf
		delete(cli.sendBuf, q.msg.SeqNum)

		// Increment sendBase until pending seqnum
		for cli.sendBase < cli.nextSeqnum {
			_, msg_exist := cli.sendBuf[cli.sendBase]
			if msg_exist {
				break
			}
			cli.sendBase++
		}

		s.sendPending(cli)

	case MsgCAck:
		if !cli_exist {
			panic("What the fuck?")
		}

		// Already received ACK or heartbeat?
		if q.msg.SeqNum < cli.sendBase {
			break
		}

		// Delete seqnum from sendBuf
		for seqNum := cli.sendBase; seqNum <= q.msg.SeqNum; seqNum++ {
			delete(cli.sendBuf, seqNum)
		}

		// Increment sendBase until pending seqnum
		for cli.sendBase < cli.nextSeqnum {
			_, msg_exist := cli.sendBuf[cli.sendBase]
			if msg_exist {
				break
			}
			cli.sendBase++
		}

		s.sendPending(cli)
	}

	cli.lastRecvEpoch = s.currEpoch
}

func (s *server) HandleCloseConn(connId int) {
	addr, exist := s.connIdToAddr[connId]
	if !exist {
		s.closeConnChan <- false
		return
	}
	cli, exist := s.client[addr]
	if !exist {
		s.closeConnChan <- false
		return
	}
	cli.status = 1
	s.closeConnChan <- true
}

func (s *server) HandleClose() {
	if s.closeEpoch == -1 {
		s.closeEpoch = s.currEpoch
	}

	unfinished := 0
	for _, cli := range s.client {
		if cli.status == 0 {
			cli.status = 1
			unfinished += 1
		} else if cli.status == 1 && len(cli.sendBuf) != 0 {
			unfinished += 1
		}
	}

	if unfinished == 0 && s.closeEpoch <= s.timeoutCloseEpoch {
		s.closeChan <- -1
	} else {
		s.closeChan <- unfinished
	}
}

/* Send ACK to client */
func (s *server) sendAck(cli *ConnectionInfo, seqNum int) error {
	ack := NewAck(cli.id, seqNum)
	ack.Checksum = CalculateChecksum(ack.ConnID, ack.SeqNum, ack.Size, ack.Payload)
	to_send, err := json.Marshal(ack)
	if err != nil {
		return err
	}
	_, err = s.conn.WriteToUDP(to_send, &cli.addr)
	if err != nil {
		return err
	}
	return nil
}

/* Send messages to client until window/unackedMessages is full */
func (s *server) sendPending(cli *ConnectionInfo) error {
	windowOccupied := cli.nextSeqnum - cli.sendBase
	unackedMessages := len(cli.sendBuf) - (cli.lastSeqnum - cli.nextSeqnum + 1)
	canSend := min(s.params.WindowSize-windowOccupied, s.params.MaxUnackedMessages-unackedMessages)
	canSend = min(canSend, cli.lastSeqnum-cli.nextSeqnum+1)
	for i := 0; i < canSend; i++ {
		cli.lastSendEpoch = s.currEpoch
		send_msg := cli.sendBuf[cli.nextSeqnum]
		send_msg.retransmitEpoch = s.currEpoch + 1
		to_send, err := json.Marshal(send_msg.msg)
		if err != nil {
			return err
		}
		_, err = s.conn.WriteToUDP(to_send, &cli.addr)
		if err != nil {
			return err
		}
		cli.nextSeqnum++
	}
	return nil
}
