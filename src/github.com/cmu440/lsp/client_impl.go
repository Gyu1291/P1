// Contains the implementation of a LSP client.

package lsp

import (
	"errors"
	"time"

	"github.com/cmu440/lspnet"
)

type client struct {
	// TODO: implement this!
	// Single connection of client
	connID                   int
	connTimer                *time.Timer
	drm                      *dataRoutineManager
	readAsyncCompleteChannel chan bool
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
	udpAddr, err := lspnet.ResolveUDPAddr("udp", hostport)
	if err != nil {
		return nil, err
	}
	// If laddr is nil, a local address is automatically chosen.
	// If the IP field of raddr is nil or an unspecified IP address, the local system is assumed.
	udpConn, err := lspnet.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, err
	}
	c := client{
		connTimer: time.NewTimer(0),
		drm:       newDataRoutineManager(udpConn, initialSeqNum, params),
	}
	c.connectRoutine()
	return &c, nil
}

func (c *client) connectRoutine() error {
	if c.drm.established {
		return errors.New("[Error]Already established")
	}
	connectMsg := NewConnect(c.drm.seqNum)
	connectionlessEpochs := 0
	c.connTimer.Reset(time.Duration(c.drm.params.EpochMillis) * time.Millisecond)
	err := sendMsgToUDP(connectMsg, c.drm.conn)
	if err != nil {
		return err
	}
	for {
		select {
		case <-c.connTimer.C:
			connectionlessEpochs++
			if connectionlessEpochs >= c.drm.params.EpochLimit {
				return errors.New("[Error]ConnectionlessEpochs reach EpochLimit")
			}
			sendMsgToUDP(connectMsg, c.drm.conn)
			c.connTimer.Reset(time.Duration(c.drm.params.EpochMillis) * time.Millisecond)
		default:
			msg, err := recvMsgFromUDP(c.drm.conn)
			if err != nil {
				return err
			}
			if msg.Type == MsgAck {
				c.connID = msg.ConnID
				c.drm.connID = msg.ConnID
				c.drm.established = true
				c.drm.seqNum++
				c.drm.oldestUnackNum = msg.SeqNum + 1
				c.connTimer.Stop()
				go c.drm.mainRoutine()
				go c.readRoutine()
			}
		}
	}
}

func (c *client) ConnID() int {
	return c.connID
}

func (c *client) Read() ([]byte, error) {

	select { // Blocks indefinitely.
	case readByte := <-c.drm.lspToAppChannel:
		go c.readRoutine()
		return readByte, nil
	}
	return nil, errors.New("not yet implemented")
}

func (c *client) Write(payload []byte) error {
	c.drm.appToLspChannel <- payload
	return nil
}

func (c *client) Close() error {
	return errors.New("not yet implemented")
}

func (c *client) readRoutine() {
	go c.readAsynchronously()
	for {
		select {
		case <-c.readAsyncCompleteChannel:
			go c.readAsynchronously()
		}
	}
}

func (c *client) readAsynchronously() {
	msg, err := recvMsgFromUDP(c.drm.conn)
	if err != nil {

	}
	c.drm.udpToLspChannel <- msg
	c.readAsyncCompleteChannel <- true
}
