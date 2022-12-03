// Contains the implementation of a LSP client.

package lsp

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/cmu440/lspnet"
)

type client struct {
	id     int // connection ID of client
	params *Params
	conn   *lspnet.UDPConn // connection to server

	currEpoch int // current time

	closeEpoch        int // epoch when Close() was called
	timeoutCloseEpoch int // epoch when server was closed by timeout

	server ConnectionInfo

	queryChan chan Query

	readChan  chan *Message
	closeChan chan int

	processingEnd chan bool
	timerEnd      chan bool
}

func connectToServer(conn *lspnet.UDPConn, hostport string, initialSeqNum int, params *Params) (int, error) {
	// Send Connect message to server
	msg := NewConnect(initialSeqNum)
	msg.Checksum = CalculateChecksum(msg.ConnID, msg.SeqNum, msg.Size, msg.Payload)
	connectMsg, err := json.Marshal(msg)
	if err != nil {
		return -1, err
	}
	_, err = conn.Write(connectMsg)
	if err != nil {
		return -1, err
	}

	ackChan := make(chan *Message)
	go receiveConnAck(conn, ackChan)

	epochCount := 0
	for {
		timer := time.NewTimer(time.Millisecond * time.Duration(params.EpochMillis))
		select {
		case <-timer.C:
			epochCount++
			if epochCount == params.EpochLimit {
				return -1, errors.New("connection timeout")
			}
			// Send Connect message to server again
			_, err := conn.Write(connectMsg)
			if err != nil {
				return -1, err
			}
		case connAck := <-ackChan:
			return connAck.ConnID, nil
		}
	}
}

func receiveConnAck(conn *lspnet.UDPConn, ackChan chan *Message) {
	buffer := make([]byte, 1500)
	for {
		n, err := conn.Read(buffer)
		if err != nil {
			continue
		}

		msg := &Message{}
		err = json.Unmarshal(buffer[:n], msg)
		if err != nil {
			continue
		}

		ackChan <- msg // received conn ack
		return
	}
}

// NewClient creates, initiates, and returns a new client. This function
// should return after a connection with the server has been established
// (i.e., the client has received an Ack message from the server in response
// to its connection request), and should return a non-nil error if a
// connection could not be made (i.e., if after K epochs, the client still
// hasn't received an Ack message from the server in response to its K
// connection requests).
//
// initialSeqNum is an int representing the Initial Sequence Number (ISN) this
// client must use. You may assume that sequence numbers do not wrap around.
//
// hostport is a colon-separated string identifying the server's host address
// and port number (i.e., "localhost:9999").
func NewClient(hostport string, initialSeqNum int, params *Params) (Client, error) {
	raddr, err := lspnet.ResolveUDPAddr("udp", hostport)
	if err != nil {
		return nil, err
	}
	conn, err := lspnet.DialUDP("udp", nil, raddr)
	if err != nil {
		return nil, err
	}

	connId, err := connectToServer(conn, hostport, initialSeqNum, params)
	if err != nil {
		conn.Close()
		return nil, err
	}

	c := &client{
		id:     connId,
		params: params,
		conn:   conn,

		currEpoch: 0,

		closeEpoch:        -1,
		timeoutCloseEpoch: -1,

		server: ConnectionInfo{
			id:   0,
			addr: *raddr,

			lastRecvEpoch: 0,
			lastSendEpoch: 0,

			sendBase:   initialSeqNum + 1,
			nextSeqnum: initialSeqNum + 1,
			lastSeqnum: initialSeqNum,
			sendBuf:    make(map[int]*SendMessage),

			recvBase: 1,
			readBase: 1,
			recvBuf:  make(map[int]*Message),
		},

		queryChan: make(chan Query),

		readChan:  make(chan *Message),
		closeChan: make(chan int),

		processingEnd: make(chan bool),
		timerEnd:      make(chan bool),
	}

	go c.ProcessQuery()
	go c.ReceivePacket()
	go c.Timer()

	return c, nil
}

func (c *client) ConnID() int {
	return c.id // no race condition right?
}

func (c *client) Read() ([]byte, error) {
	for {
		c.queryChan <- Query{Type: Read}
		msg := <-c.readChan
		if msg.ConnID != -1 {
			if msg.SeqNum == -1 {
				return nil, errors.New("server disconnected")
			}
			return msg.Payload, nil
		}
	}
}

func (c *client) Write(payload []byte) error {
	msg := &Message{
		Type:    MsgData,
		ConnID:  c.id,
		Size:    len(payload),
		Payload: payload,
		// SeqNum, Checksum will be written in HandleWrite()
	}
	c.queryChan <- Query{Type: Write, msg: msg} // could block..???
	return nil
}

func (c *client) Close() error {
	for {
		c.queryChan <- Query{Type: Close}
		status := <-c.closeChan
		if status > 0 {
			continue
		}

		c.processingEnd <- true // shutdown ProcessQuery()
		c.timerEnd <- true      // shutdown Timer()
		c.conn.Close()          // shutdown ReceivePacket()

		if status < 0 {
			return errors.New("server lost connection while trying to close")
		}
		return nil
	}
}

func (c *client) ProcessQuery() {
	for {
		select {
		case <-c.processingEnd:
			return
		case q := <-c.queryChan:
			switch q.Type {
			case Read:
				c.HandleRead()
			case Write:
				c.HandleWrite(&q)
			case Timer:
				c.HandleTimer()
			case Receive:
				c.HandleReceive(&q)
			case Close:
				c.HandleClose()
			}
		}
	}
}

func (c *client) ReceivePacket() {
	buffer := make([]byte, 1500)
	for {
		n, err := c.conn.Read(buffer)
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

		c.queryChan <- Query{Type: Receive, msg: msg}
	}
}

func (c *client) Timer() {
	for {
		timer := time.NewTimer(time.Millisecond * time.Duration(c.params.EpochMillis))
		select {
		case <-c.timerEnd:
			return
		case <-timer.C:
			c.queryChan <- Query{Type: Timer}
		}
	}
}

func (c *client) HandleRead() {
	// Check if we can read some message in order
	msg, exist := c.server.recvBuf[c.server.readBase]
	if exist {
		c.readChan <- msg
		delete(c.server.recvBuf, c.server.readBase)
		c.server.readBase++
		return
	}

	// Tell Read() that server is closed
	if (c.server.status == 1 && len(c.server.sendBuf) == 0) || c.server.status == 2 {
		c.server.status = 3
		c.readChan <- &Message{Type: MsgData, SeqNum: -1}
		return
	}

	// failed to read... make Read() request again
	c.readChan <- &Message{Type: MsgData, ConnID: -1}
}

func (c *client) HandleWrite(q *Query) {
	c.server.lastSendEpoch = c.currEpoch
	c.server.lastSeqnum++

	q.msg.SeqNum = c.server.lastSeqnum
	q.msg.Checksum = CalculateChecksum(q.msg.ConnID, q.msg.SeqNum, q.msg.Size, q.msg.Payload)

	c.server.sendBuf[q.msg.SeqNum] = &SendMessage{
		retransmitEpoch: -1,
		currentBackoff:  0,
		msg:             q.msg,
	}

	c.sendPending()
}

func (c *client) HandleTimer() {
	c.currEpoch++

	if c.server.status >= 2 {
		return
	}

	// close connection if it exceeded epoch limit
	if c.currEpoch-c.server.lastRecvEpoch >= c.params.EpochLimit {
		c.server.status = 2
		return
	}

	// send heartbeat
	if c.server.status == 0 && c.server.lastSendEpoch < c.currEpoch-1 {
		c.sendAck(0)
	}

	// retransmit sent but unacked messages
	for _, send_msg := range c.server.sendBuf {
		if send_msg.retransmitEpoch == -1 {
			continue // this message has not been sent yet!
		} else if send_msg.retransmitEpoch <= c.currEpoch {
			to_send, err := json.Marshal(send_msg.msg)
			if err != nil {
				continue
			}
			_, err = c.conn.Write(to_send)
			if err != nil {
				continue
			}
			send_msg.currentBackoff = nextBackoff(send_msg.currentBackoff, c.params.MaxBackOffInterval)
			send_msg.retransmitEpoch = c.currEpoch + send_msg.currentBackoff + 1
		}
	}
}

func (c *client) HandleReceive(q *Query) {
	switch q.msg.Type {
	case MsgData:
		c.sendAck(q.msg.SeqNum)

		// Already received...!?
		_, msg_exist := c.server.recvBuf[q.msg.SeqNum]
		if q.msg.SeqNum < c.server.recvBase || msg_exist {
			break
		}

		// increment recvBase if possible
		c.server.recvBuf[q.msg.SeqNum] = q.msg
		for {
			_, msg_exist := c.server.recvBuf[c.server.recvBase]
			if !msg_exist {
				break
			}
			c.server.recvBase++
		}

	case MsgAck:
		// Already received ACK or heartbeat?
		if q.msg.SeqNum < c.server.sendBase {
			break
		}

		delete(c.server.sendBuf, q.msg.SeqNum)

		for c.server.sendBase < c.server.nextSeqnum {
			_, msg_exist := c.server.sendBuf[c.server.sendBase]
			if msg_exist {
				break
			}
			c.server.sendBase++
		}

		c.sendPending()

	case MsgCAck:
		// Already received ACK or heartbeat?
		if q.msg.SeqNum < c.server.sendBase {
			break
		}

		for seqNum := c.server.sendBase; seqNum <= q.msg.SeqNum; seqNum++ {
			delete(c.server.sendBuf, seqNum)
		}

		for c.server.sendBase < c.server.nextSeqnum {
			_, msg_exist := c.server.sendBuf[c.server.sendBase]
			if msg_exist {
				break
			}
			c.server.sendBase++
		}

		c.sendPending()
	}

	c.server.lastRecvEpoch = c.currEpoch
}

func (c *client) HandleClose() {
	if c.closeEpoch == -1 {
		c.closeEpoch = c.currEpoch
	}

	unfinished := 0
	if c.server.status == 0 {
		c.server.status = 1
		unfinished = 1
	} else if c.server.status == 1 && len(c.server.sendBuf) != 0 {
		unfinished = 1
	}

	if unfinished == 0 && c.closeEpoch <= c.timeoutCloseEpoch {
		c.closeChan <- -1
	} else {
		c.closeChan <- unfinished
	}
}

/* Send ACK to server */
func (c *client) sendAck(seqNum int) error {
	ack := NewAck(c.id, seqNum)
	ack.Checksum = CalculateChecksum(ack.ConnID, ack.SeqNum, ack.Size, ack.Payload)
	to_send, err := json.Marshal(ack)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(to_send)
	if err != nil {
		return err
	}
	return nil
}

/* Send messages to server until window/unackedMessages is full */
func (c *client) sendPending() error {
	windowOccupied := c.server.nextSeqnum - c.server.sendBase
	unackedMessages := len(c.server.sendBuf) - (c.server.lastSeqnum - c.server.nextSeqnum + 1)
	canSend := min(c.params.WindowSize-windowOccupied, c.params.MaxUnackedMessages-unackedMessages)
	canSend = min(canSend, c.server.lastSeqnum-c.server.nextSeqnum+1)
	for i := 0; i < canSend; i++ {
		send_msg := c.server.sendBuf[c.server.nextSeqnum]
		send_msg.retransmitEpoch = c.currEpoch + 1
		to_send, err := json.Marshal(send_msg.msg)
		if err != nil {
			return err
		}
		_, err = c.conn.Write(to_send)
		if err != nil {
			return err
		}
		c.server.nextSeqnum++
	}
	return nil
}
