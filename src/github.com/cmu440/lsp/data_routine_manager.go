package lsp

import (
	"errors"
	"fmt"
	"time"

	"github.com/cmu440/lspnet"
)

const receivingWindowSize int = 1000

type dataRoutineManager struct {
	// connection management
	isServer         bool
	connID           int
	conn             *lspnet.UDPConn
	addr             *lspnet.UDPAddr
	established      bool
	unreceivedEpochs int

	// Sender's seqNum
	seqNum int

	// Number of inflight packets
	slidingWindow []*Message
	// Mapping unacked message to its current backoff
	currentBackOff map[*Message]*backOffState

	// Receiver's oldest unacked seqNum
	oldestUnackNum  int
	receivingWindow [receivingWindowSize]*Message

	timer *time.Timer

	params *Params

	appToLspChannel chan []byte
	lspToAppChannel chan []byte

	udpToLspChannel         chan *Message
	errorSignalInLspChannel chan error
	errorLspToAppChannel    chan error

	closingStartChannel    chan bool
	closingCompleteChannel chan bool

	quickCloseLspToAppChannel chan error
	quickCloseAppToLspChannel chan bool
}

type backOffState struct {
	currentEpoch   int
	currentBackOff int
	nextRetransmit int
}

func newDataRoutineManager(conn *lspnet.UDPConn, seqNum int, params *Params) *dataRoutineManager {
	drm := dataRoutineManager{
		conn:                      conn,
		established:               false,
		seqNum:                    seqNum,
		slidingWindow:             make([]*Message, 0, params.WindowSize),
		currentBackOff:            make(map[*Message]*backOffState),
		timer:                     time.NewTimer(0),
		params:                    params,
		appToLspChannel:           make(chan []byte, 10),
		lspToAppChannel:           make(chan []byte, 10),
		udpToLspChannel:           make(chan *Message, 10),
		errorSignalInLspChannel:   make(chan error, 1),
		errorLspToAppChannel:      make(chan error, 1),
		closingStartChannel:       make(chan bool, 1),
		closingCompleteChannel:    make(chan bool, 1),
		quickCloseLspToAppChannel: make(chan error, 1),
		quickCloseAppToLspChannel: make(chan bool, 1),
	}
	return &drm
}

func (d *dataRoutineManager) mainRoutine() {
	d.timer.Reset(time.Duration(d.params.EpochMillis) * time.Millisecond)
	for {
		select {
		case err := <-d.errorSignalInLspChannel:
			fmt.Println("Signal to errorSignalInLspChannel in mainRoutine")
			d.errorLspToAppChannel <- err
			return
		case <-d.quickCloseAppToLspChannel:
			fmt.Println("Signal to quickCloseAppToLspChannel in mainRoutine")
			return
		case <-d.closingStartChannel:
			d.timer.Stop()
			for { // Blocks until all pending msgs from the other side are acked
				if len(d.slidingWindow) == 0 {
					d.closingCompleteChannel <- true
					return
				}
				select {
				case readMsg := <-d.udpToLspChannel:
					switch readMsg.Type {
					case MsgAck:
						d.processMsgAckLspToApp(readMsg)
					case MsgCAck:
						d.processMsgCAckLspToApp(readMsg)
					}
				}
			}
		case <-d.timer.C:
			d.updateBackOff()
			d.unreceivedEpochs++
			if d.unreceivedEpochs >= d.params.EpochLimit {
				d.quickCloseLspToAppChannel <- errors.New("reach EpochLimit")
				return
			}
			d.timer.Reset(time.Duration(d.params.EpochMillis) * time.Millisecond)
		case databyteToSend := <-d.appToLspChannel:
			if len(d.slidingWindow) <= d.params.WindowSize && len(d.currentBackOff) <= d.params.MaxUnackedMessages {
				// First transmit
				msgToSend := NewData(d.connID, d.seqNum, len(databyteToSend), databyteToSend,
					CalculateChecksum(d.connID, d.seqNum, len(databyteToSend), databyteToSend))
				d.seqNum++
				d.sendMsgLspToUdp(msgToSend, false)
			}
		case readMsg := <-d.udpToLspChannel:
			d.unreceivedEpochs = 0
			switch readMsg.Type {
			case MsgData:
				d.processMsgDataLspToApp(readMsg)
			case MsgAck:
				d.processMsgAckLspToApp(readMsg)
			case MsgCAck:
				d.processMsgCAckLspToApp(readMsg)
			}
		}

	}
}

func (d *dataRoutineManager) processMsgDataLspToApp(msg *Message) {
	if msg.SeqNum >= d.oldestUnackNum && msg.SeqNum-d.oldestUnackNum < receivingWindowSize {
		d.receivingWindow[msg.SeqNum-d.oldestUnackNum] = msg
		if msg.SeqNum > d.oldestUnackNum {
			ack := NewAck(d.connID, msg.SeqNum)
			d.sendMsgLspToUdp(ack, false)
		} else {
			// msg.Seq == d.oldestUnackNum
			// In-order delivery to app layer
			index := 0
			for _, m := range d.receivingWindow {
				if m == nil {
					break
				} else {
					d.lspToAppChannel <- msg.Payload
					d.oldestUnackNum++
					index++
				}
			}
			for i := 0; i < receivingWindowSize-index-1; i++ {
				d.receivingWindow[i] = d.receivingWindow[i+index]
			}
			for i := receivingWindowSize - index; i < receivingWindowSize; i++ {
				d.receivingWindow[i] = nil
			}
			cAck := NewCAck(d.connID, d.oldestUnackNum-1)
			d.sendMsgLspToUdp(cAck, false)
		}
	}
}

func (d *dataRoutineManager) processMsgAckLspToApp(msg *Message) {
	for i, m := range d.slidingWindow {
		if msg.SeqNum == m.SeqNum {
			delete(d.currentBackOff, m)
			if i == 0 {
				if len(d.slidingWindow) <= 1 {
					d.slidingWindow = make([]*Message, 0)
				} else {
					d.slidingWindow = d.slidingWindow[1:]
				}
			}
		}
	}
}

func (d *dataRoutineManager) processMsgCAckLspToApp(msg *Message) {
	index := -1
	for i, m := range d.slidingWindow {
		if msg.SeqNum == m.SeqNum {
			index = i
			break
		}
	}
	if index != -1 {
		for i := 0; i <= index; i++ {
			delete(d.currentBackOff, d.slidingWindow[i])
		}
		if len(d.slidingWindow) == index+1 {
			d.slidingWindow = make([]*Message, 0)
		} else {
			d.slidingWindow = d.slidingWindow[index+1:]
		}
	}
}

func (d *dataRoutineManager) sendMsgLspToUdp(msgToSend *Message, isRetx bool) {
	var err error
	if d.isServer {
		err = sendMsgToUDPWithAddr(msgToSend, d.conn, d.addr)
		// fmt.Println("Server sendMsgLspToUdp : ", msgToSend)
	} else {
		err = sendMsgToUDP(msgToSend, d.conn)
		// fmt.Println("Client sendMsgLspToUdp : ", msgToSend)
	}
	if err != nil {
		d.errorSignalInLspChannel <- err
		return
	}
	// MsgData First transmit (Not retx)
	if !isRetx && msgToSend.Type == MsgData {
		d.slidingWindow = append(d.slidingWindow, msgToSend)
		d.currentBackOff[msgToSend] = &backOffState{currentEpoch: 0, currentBackOff: 0, nextRetransmit: 1}
	}
}

func (d *dataRoutineManager) updateBackOff() {
	transmitted := false
	for _, m := range d.slidingWindow {
		backOff, exists := d.currentBackOff[m]
		if exists {
			backOff.currentEpoch++
			if backOff.currentEpoch == backOff.nextRetransmit {
				// Retransmit
				d.sendMsgLspToUdp(m, true)
				transmitted = true
				if backOff.currentBackOff == 0 {
					backOff.currentBackOff = 1
				} else {
					backOff.currentBackOff *= 2
				}
				if backOff.currentBackOff >= d.params.MaxBackOffInterval {
					backOff.currentBackOff = d.params.MaxBackOffInterval
				}
				backOff.nextRetransmit = backOff.currentEpoch + 1 + backOff.currentBackOff
			}
		}
	}
	if !transmitted {
		d.sendHeartBeat()
	}
}

func (d *dataRoutineManager) sendHeartBeat() {
	ack := NewAck(d.connID, 0)
	d.sendMsgLspToUdp(ack, true)
}
