// Contains the implementation of a LSP server.

package lsp

import (
	"errors"

	"github.com/cmu440/lspnet"
)

type server struct {
	// TODO: Implement this!
	listenConn *lspnet.UDPConn
	// Multiple connections from Multiple clients
	connIDs []int

	/* key = connID */
	conns           map[int]*lspnet.UDPConn
	seqNums         map[int]int
	expectedSeqNums map[int]int
}

// NewServer creates, initiates, and returns a new server. This function should
// NOT block. Instead, it should spawn one or more goroutines (to handle things
// like accepting incoming client connections, triggering epoch events at
// fixed intervals, synchronizing events using a for-select loop like you saw in
// project 0, etc.) and immediately return. It should return a non-nil error if
// there was an error resolving or listening on the specified port number.
const hostip string = "127.0.0.1"

func NewServer(port int, params *Params) (Server, error) {
	hostPort := lspnet.JoinHostPort(hostip, string(port))
	udpAddr, err := lspnet.ResolveUDPAddr("udp", hostPort)
	if err != nil {
		return nil, errors.New("Resolve UDP Address failed")
	}
	listenConn, err := lspnet.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, errors.New("UDP connection failed")
	}
	s := server{listenConn: listenConn}
	return &s, nil

func (s *server) Read() (int, []byte, error) {
	
	select {} // Blocks indefinitely.
	return -1, nil, errors.New("not yet implemented")
}

func (s *server) Write(connId int, payload []byte) error {
	return errors.New("not yet implemented")
}

func (s *server) CloseConn(connId int) error {
	return errors.New("not yet implemented")
}

func (s *server) Close() error {
	return errors.New("not yet implemented")
}
