// Contains the implementation of a LSP client.

package lsp

import (
	"encoding/json"
	"errors"

	"github.com/cmu440/lspnet"
)

type client struct {
	conn       *lspnet.UDPConn
	connId     int
	payload    []byte
	message    chan *Message
	quitSignal chan bool
	// TODO: implement this!
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
	cli := &client{conn: nil}
	addr, err := lspnet.ResolveUDPAddr("udp", hostport) //(udp or udp4 or udp6) + port string
	if err != nil {
		return nil, err
	}

	conn, err := lspnet.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, err
	}

	cli.conn = conn

	go cli.MainRoutine()
	go cli.ReadRoutine()

	return cli, nil

}

func (c *client) ConnID() int {
	return c.connId
}

func (c *client) Read() ([]byte, error) {
	// TODO: remove this line when you are ready to begin implementing this method.
	select {} // Blocks indefinitely.
}

func (c *client) Write(payload []byte) error {
	return errors.New("not yet implemented")
}

func (c *client) Close() error {
	return nil
}

func (c *client) MainRoutine() {
	for {
		select {
		case <-c.quitSignal:
			return
		case m := <-c.message:
			c.handleMessage(m)

		}
	}
}

func (c *client) ReadRoutine() {
	for {
		select {
		case <-c.quitSignal:
			return
		default:
			var bytes [2000]byte
			n, err := c.conn.Read(bytes[:])
			if err != nil {
				return
			}
			var m Message
			merr := json.Unmarshal(bytes[:n], &m)
			if merr != nil {
				return
			}

			c.message <- &m

		}
	}
}

// MsgConnect MsgData MsgAck MsgCAck

func (c *client) handleMessage(m *Message) {
	msgType := m.Type
	switch msgType {
	case 0:

	case 1:
		//Data
	case 2:
		//Ack
	case 3:
		//CAck
	}
}
