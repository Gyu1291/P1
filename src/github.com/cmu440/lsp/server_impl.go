// Contains the implementation of a LSP server.

package lsp

import (
	"errors"
	"fmt"

	"github.com/cmu440/lspnet"
)

type server struct {
	listenConn *lspnet.UDPConn
	newConnID  int
	params     *Params

	hostPortToConnIDs map[string]int
	connIDs           []int
	connIDToDrm       map[int]*dataRoutineManager

	serverMsgUdpToLspChannel  chan *serverReadMsg
	serverByteLspToAppChannel chan *serverReadByteFromLsp
	serverErrorChannel        chan error
}

type serverReadMsg struct {
	msg  *Message
	addr *lspnet.UDPAddr
}

type serverReadByteFromLsp struct {
	readByte []byte
	connID   int
}

// NewServer creates, initiates, and returns a new server. This function should
// NOT block. Instead, it should spawn one or more goroutines (to handle things
// like accepting incoming client connections, triggering epoch events at
// fixed intervals, synchronizing events using a for-select loop like you saw in
// project 0, etc.) and immediately return. It should return a non-nil error if
// there was an error resolving or listening on the specified port number.
const hostip string = "127.0.0.1"

func NewServer(port int, params *Params) (Server, error) {
	hostPort := lspnet.JoinHostPort(hostip, fmt.Sprintf("%d", port))
	udpAddr, err := lspnet.ResolveUDPAddr("udp", hostPort)
	if err != nil {
		return nil, err
	}
	listenConn, err := lspnet.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}
	s := server{
		listenConn:                listenConn,
		newConnID:                 1,
		hostPortToConnIDs:         make(map[string]int),
		connIDs:                   make([]int, 0),
		connIDToDrm:               make(map[int]*dataRoutineManager),
		serverMsgUdpToLspChannel:  make(chan *serverReadMsg),
		serverByteLspToAppChannel: make(chan *serverReadByteFromLsp, 10),
		serverErrorChannel:        make(chan error),
		params:                    params,
	}
	go s.readRoutine()
	go s.readFromUDPRoutine()
	return &s, nil
}

func (s *server) Read() (int, []byte, error) {
	select {
	case readData := <-s.serverByteLspToAppChannel:
		return readData.connID, readData.readByte, nil
	}
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

func (s *server) readFromUDPRoutine() {
	for {
		select {
		default:
			msg, addr, err := recvMsgWithAddrFromUDP(s.listenConn)
			if err != nil {
				s.serverErrorChannel <- err
			}
			srm := serverReadMsg{
				msg:  msg,
				addr: addr,
			}
			s.serverMsgUdpToLspChannel <- &srm
		}
	}
}

// Demultiplexing from multiple lsp clients
func (s *server) readFromLSPRoutine() {
	for {
		for _, id := range s.connIDs {
			drm := s.connIDToDrm[id]
			select {
			case readByte := <-drm.lspToAppChannel:
				s.serverByteLspToAppChannel <- &serverReadByteFromLsp{
					readByte: readByte,
					connID:   id,
				}
			}
		}
	}
}

func (s *server) readRoutine() {
	for {
		select {
		case readMsg := <-s.serverMsgUdpToLspChannel:
			msg := readMsg.msg
			addrStr := readMsg.addr.String()
			switch msg.Type {
			case MsgConnect:
				if _, exists := s.hostPortToConnIDs[addrStr]; !exists {
					s.hostPortToConnIDs[addrStr] = s.newConnID
					s.connIDs = append(s.connIDs, s.newConnID)
					s.connIDToDrm[s.newConnID] = newDataRoutineManager(s.listenConn, s.newConnID*1000, s.params)
					drm := s.connIDToDrm[s.newConnID]
					go drm.mainRoutine()
					ack := NewAck(drm.connID, drm.seqNum)
					err := sendMsgToUDP(ack, drm.conn)
					if err != nil {
						s.serverErrorChannel <- err
					}
					s.newConnID++
				}
			case MsgData:
			case MsgAck:
			case MsgCAck:
				if connID, exists := s.hostPortToConnIDs[addrStr]; exists && msg.ConnID == connID {
					drm := s.connIDToDrm[connID]
					drm.udpToLspChannel <- msg
				}
			}
		}
	}
}
