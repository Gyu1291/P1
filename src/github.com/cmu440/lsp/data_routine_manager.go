package lsp

import (
	"encoding/json"
	"time"

	"github.com/cmu440/lspnet"
)

const receivingWindowSize int = 10

type dataRoutineManager struct {
	// connection management
	connID      int
	conn        *lspnet.UDPConn
	established bool

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

	udpToLspChannel chan *Message
}

type backOffState struct {
	currentEpoch   int
	currentBackOff int
	nextRetransmit int
}

func newDataRoutineManager(conn *lspnet.UDPConn, seqNum int, params *Params) *dataRoutineManager {
	drm := dataRoutineManager{
		conn:            conn,
		established:     false,
		seqNum:          seqNum,
		slidingWindow:   make([]*Message, 0, params.WindowSize),
		currentBackOff:  make(map[*Message]*backOffState),
		timer:           time.NewTimer(0),
		params:          params,
		appToLspChannel: make(chan []byte, 10),
		lspToAppChannel: make(chan []byte, 10),
		udpToLspChannel: make(chan *Message, 1),
	}
	return &drm
}

func (d *dataRoutineManager) mainRoutine() {
	d.timer.Reset(time.Duration(d.params.EpochMillis) * time.Millisecond)
	for {
		select {
		case <-d.timer.C:
			d.updateBackOff()
			d.timer.Reset(time.Duration(d.params.EpochMillis) * time.Millisecond)
		case databyteToSend := <-d.appToLspChannel:
			if len(d.slidingWindow) <= d.params.WindowSize && len(d.currentBackOff) <= d.params.MaxUnackedMessages {
				// First transmit
				msgToSend := NewData(d.connID, d.seqNum, len(databyteToSend), databyteToSend,
					CalculateChecksum(d.connID, d.seqNum, len(databyteToSend), databyteToSend))
				go d.sendMsgToUDPAsync(msgToSend, false)
			}
		case readMsg := <-d.udpToLspChannel:
			switch readMsg.Type {
			case MsgData:
				go d.processMsgDataLspToApp(readMsg)
			case MsgAck:
				go d.processMsgAckLspToApp(readMsg)
			case MsgCAck:
				go d.processMsgCAckLspToApp(readMsg)
			}
		}

	}
}

func (d *dataRoutineManager) processMsgDataLspToApp(msg *Message) {
	if msg.SeqNum >= d.oldestUnackNum && msg.SeqNum-d.oldestUnackNum < receivingWindowSize {
		d.receivingWindow[msg.SeqNum-d.oldestUnackNum] = msg
		if msg.SeqNum > d.oldestUnackNum {
			ack := NewAck(d.connID, msg.SeqNum)
			go d.sendMsgToUDPAsync(ack, false)
		} else {
			// msg.Seq == d.oldestUnackNum
			// In-order delivery to app layer
			index := 0
			for _, m := range d.receivingWindow {
				if m == nil {
					break
				} else {
					msgBytes, err := json.Marshal(msg)
					if err != nil {

					}
					d.lspToAppChannel <- msgBytes
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
			go d.sendMsgToUDPAsync(cAck, false)
		}
	}
}

func (d *dataRoutineManager) processMsgAckLspToApp(msg *Message) {

}

func (d *dataRoutineManager) processMsgCAckLspToApp(msg *Message) {

}

func (d *dataRoutineManager) sendMsgToUDPAsync(msgToSend *Message, isRetx bool) {
	err := sendMsgToUDP(msgToSend, d.conn)
	if err != nil {

	}
	// MsgData Retx
	if !isRetx {
		d.slidingWindow = append(d.slidingWindow, msgToSend)
		d.currentBackOff[msgToSend] = &backOffState{currentEpoch: 0, currentBackOff: 0, nextRetransmit: 1}
	}
}

func (d *dataRoutineManager) updateBackOff() {
	for _, m := range d.slidingWindow {
		backOff, exists := d.currentBackOff[m]
		if exists {
			backOff.currentEpoch++
			if backOff.currentEpoch > d.params.EpochLimit {
				// connection close
				return
			}
			if backOff.currentEpoch == backOff.nextRetransmit {
				// Retransmit
				go d.sendMsgToUDPAsync(m, true)
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
}
