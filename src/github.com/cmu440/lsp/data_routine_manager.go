package lsp

import (
	"time"

	"github.com/cmu440/lspnet"
)

type dataRoutineManager struct {
	// connection management
	connID      int
	conn        *lspnet.UDPConn
	established bool

	seqNum         int
	oldestUnackNum int
	unackedMsgs    []*Message

	timer          *time.Timer
	currentBackOff int

	params *Params

	appToLspChannel chan []byte
	lspToAppChannel chan []byte
}

func newDataRoutineManager(conn *lspnet.UDPConn, seqNum int, params *Params) *dataRoutineManager {
	drm := dataRoutineManager{
		conn:            conn,
		established:     false,
		seqNum:          seqNum,
		timer:           time.NewTimer(0),
		currentBackOff:  0,
		params:          params,
		appToLspChannel: make(chan []byte, 1),
		lspToAppChannel: make(chan []byte, 1),
	}
	return &drm
}

func (d *dataRoutineManager) mainRoutine() {
	d.timer.Reset(time.Duration(d.params.EpochMillis) * time.Millisecond)
	for {
		select {
		case dataToSend := <-d.appToLspChannel:
			if len(d.unackedMsgs) <= d.params.MaxUnackedMessages {
				msgToSend := NewData(d.connID, d.seqNum, len(dataToSend), dataToSend,
					CalculateChecksum(d.connID, d.seqNum, len(dataToSend), dataToSend))
				err := sendMsgToUDP(msgToSend, d.conn)
				if err != nil {

				}
			}

		}
	}
}
