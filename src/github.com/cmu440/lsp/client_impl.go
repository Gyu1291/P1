// Contains the implementation of a LSP client.

package lsp

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/cmu440/lspnet"
)

type client struct {
	// TODO: implement this!
	// Single connection of client
	connID        int
	conn          *lspnet.UDPConn
	established   bool
	initialSeqNum int
	connTimer     *time.Timer
	params        *Params
	drm           *dataRoutineManager
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
		conn:        udpConn,
		established: false,
		connTimer:   time.NewTimer(0),
		params:      params,
	}
	c.connectRoutine()

	return &c, nil
}

func (c *client) connectRoutine() error {
	if c.established {
		return errors.New("[Error]Already established")
	}
	connectMsg := NewConnect(c.initialSeqNum)
	connectionlessEpochs := 0
	c.connTimer.Reset(time.Duration(c.params.EpochMillis) * time.Millisecond)
	err := c.sendMsgToNetwork(connectMsg)
	if err != nil {
		return err
	}
	for {
		select {
		case <-c.connTimer.C:
			connectionlessEpochs++
			if connectionlessEpochs >= c.params.EpochLimit {
				return errors.New("[Error]ConnectionlessEpochs reach EpochLimit")
			}
			c.sendMsgToNetwork(connectMsg)
			c.connTimer.Reset(time.Duration(c.params.EpochMillis) * time.Millisecond)
		default:
			msg, err := c.recvMsgFromNetwork()
			if err != nil {
				return err
			}
			if msg.Type == MsgAck {
				c.connID = msg.ConnID
				c.established = true
				c.connTimer.Stop()
			}
		}
	}
}

func (c *client) recvMsgFromNetwork() (*Message, error) {
	var receivedMsgBytes []byte
	numBytes, err := c.conn.Read(receivedMsgBytes)
	if err != nil {
		return nil, err
	}
	var receivedMsg *Message
	err = json.Unmarshal(receivedMsgBytes[:numBytes], receivedMsg)
	if err != nil {
		return nil, err
	}
	return receivedMsg, nil
}

func (c *client) sendMsgToNetwork(msg *Message) error {
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(msgBytes)
	if err != nil {
		return err
	}
	return nil
}

func (c *client) ConnID() int {
	return c.connID
}

func (c *client) Read() ([]byte, error) {
	// TODO: remove this line when you are ready to begin implementing this method.
	select {} // Blocks indefinitely.
	return nil, errors.New("not yet implemented")
}

func (c *client) Write(payload []byte) error {
	return errors.New("not yet implemented")
}

func (c *client) Close() error {
	return errors.New("not yet implemented")
}
