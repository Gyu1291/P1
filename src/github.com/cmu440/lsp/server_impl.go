// Contains the implementation of a LSP server.

package lsp

import (
	"errors"
	"fmt"
	"time"

	"github.com/cmu440/lspnet"
)

type server struct {
	listenConn *lspnet.UDPConn
	newConnID  int
	params     *Params

	hostPortToConnIDs map[string]int
	connIDsTohostPort map[int]string
	connIDs           []int
	connIDToDrm       map[int]*dataRoutineManager

	serverMsgUdpToLspChannel        chan *serverReadMsg
	serverByteLspToAppChannel       chan *serverReadByteFromLsp
	serverErrorInLspChannel         chan error
	serverErrorLspToAppChannel      chan error
	connErrorLspToAppChannel        chan *oneConnErrorInfo
	closeReadFromUdpRoutineChannel  chan bool
	closeReadFromLspRoutineChannel  chan bool
	closeReadRoutineChannel         chan bool
	closeCompleteReadRoutineChannel chan bool
}

type serverReadMsg struct {
	msg  *Message
	addr *lspnet.UDPAddr
}

type serverReadByteFromLsp struct {
	readByte []byte
	connID   int
}

type oneConnErrorInfo struct {
	connID int
	err    error
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
		listenConn:                      listenConn,
		newConnID:                       1,
		hostPortToConnIDs:               make(map[string]int),
		connIDs:                         make([]int, 0),
		connIDsTohostPort:               make(map[int]string),
		connIDToDrm:                     make(map[int]*dataRoutineManager),
		serverMsgUdpToLspChannel:        make(chan *serverReadMsg),
		serverByteLspToAppChannel:       make(chan *serverReadByteFromLsp, 10),
		serverErrorInLspChannel:         make(chan error),
		serverErrorLspToAppChannel:      make(chan error),
		connErrorLspToAppChannel:        make(chan *oneConnErrorInfo),
		closeReadRoutineChannel:         make(chan bool),
		closeCompleteReadRoutineChannel: make(chan bool),
		closeReadFromUdpRoutineChannel:  make(chan bool),
		closeReadFromLspRoutineChannel:  make(chan bool),
		params:                          params,
	}
	go s.readRoutine()
	go s.readFromUDPRoutine()
	go s.readFromLSPRoutine()
	return &s, nil
}

func (s *server) Read() (int, []byte, error) {
	select { // Blocking
	case err := <-s.serverErrorLspToAppChannel:
		return -1, nil, err
	case oneConnErr := <-s.connErrorLspToAppChannel:
		return oneConnErr.connID, nil, oneConnErr.err
	case readData := <-s.serverByteLspToAppChannel:
		return readData.connID, readData.readByte, nil
	}
}

func (s *server) Write(connId int, payload []byte) error {
	select { // Non-blocking
	case err := <-s.serverErrorLspToAppChannel:
		return err
	default:
		drm, exists := s.connIDToDrm[connId]
		if !exists {
			return errors.New("Connection ID Not Found")
		}
		drm.appToLspChannel <- payload
		return nil
	}
}

func (s *server) CloseConn(connId int) error {
	drm := s.connIDToDrm[connId]
	drm.closingStartChannel <- true
	select {
	case err := <-s.serverErrorLspToAppChannel:
		return err
	case <-drm.closingCompleteChannel:
		s.removeOneConnInfo(connId)
		return nil
	}
}

func (s *server) Close() error {
	for _, drm := range s.connIDToDrm {
		drm.closingCompleteChannel <- true
	}
	for _, drm := range s.connIDToDrm {
		select {
		case err := <-s.serverErrorLspToAppChannel:
			return err
		case <-drm.closingCompleteChannel:
			s.removeOneConnInfo(drm.connID)
		}
	}
	s.closeReadRoutineChannel <- true
	select {
	case err := <-s.serverErrorLspToAppChannel:
		return err
	case <-s.closeCompleteReadRoutineChannel:
		return nil
	}
}

func (s *server) readFromUDPRoutine() {
	for {
		select {
		case <-s.closeReadFromUdpRoutineChannel:
			return
		default:
			msg, addr, err := recvMsgWithAddrFromUDP(s.listenConn)
			if err != nil {
				continue
			}
			srm := serverReadMsg{
				msg:  msg,
				addr: addr,
			}
			s.serverMsgUdpToLspChannel <- &srm
		}
	}
}

// One routine for one server
// Demultiplexing from multiple lsp clients
// Also deal with removing one connection info
func (s *server) readFromLSPRoutine() {
	for {
		for _, id := range s.connIDs {
			time.Sleep(time.Millisecond)
			drm := s.connIDToDrm[id]
			select {
			case <-s.closeReadFromLspRoutineChannel:
				return
			case err := <-drm.errorLspToAppChannel:
				// After mainRoutine of drm is terminated
				s.removeOneConnInfo(id)
				s.connErrorLspToAppChannel <- &oneConnErrorInfo{err: err, connID: id}
			case err := <-drm.quickCloseLspToAppChannel:
				// After mainRoutine of drm is terminated
				s.removeOneConnInfo(id)
				s.connErrorLspToAppChannel <- &oneConnErrorInfo{err: err, connID: id}
			case readByte := <-drm.lspToAppChannel:
				s.serverByteLspToAppChannel <- &serverReadByteFromLsp{
					readByte: readByte,
					connID:   id,
				}
			default:
				continue
			}
		}
	}
}

func (s *server) removeOneConnInfo(connID int) {
	addrStr := s.connIDsTohostPort[connID]
	delete(s.connIDsTohostPort, connID)
	delete(s.hostPortToConnIDs, addrStr)
	index := 0
	for i, id := range s.connIDs {
		if id == connID {
			index = i
		}
	}
	switch index {
	case 0:
		s.connIDs = s.connIDs[1:]
	case len(s.connIDs) - 1:
		s.connIDs = s.connIDs[:index]
	default:
		s.connIDs = append(s.connIDs[:index], s.connIDs[index+1:]...)
	}
}

// One routine per one connection -> Multiple routines for one server
func (s *server) readRoutine() {
	for {
		select {
		case <-s.closeReadRoutineChannel:
			s.closeReadFromUdpRoutineChannel <- true
			s.closeReadFromLspRoutineChannel <- true
			s.closeCompleteReadRoutineChannel <- true
			return
		case err := <-s.serverErrorInLspChannel:
			for _, id := range s.connIDs {
				drm := s.connIDToDrm[id]
				drm.quickCloseAppToLspChannel <- true
				s.removeOneConnInfo(id)
			}
			s.closeReadFromUdpRoutineChannel <- true
			s.closeReadFromLspRoutineChannel <- true
			s.serverErrorLspToAppChannel <- err
			return
		case readMsg := <-s.serverMsgUdpToLspChannel:
			msg := readMsg.msg
			addrStr := readMsg.addr.String()
			switch msg.Type {
			case MsgConnect:
				if _, exists := s.hostPortToConnIDs[addrStr]; !exists {
					s.hostPortToConnIDs[addrStr] = s.newConnID
					s.connIDsTohostPort[s.newConnID] = addrStr
					s.connIDs = append(s.connIDs, s.newConnID)
					s.connIDToDrm[s.newConnID] = newDataRoutineManager(s.listenConn, s.newConnID*1000, s.params)
					drm := s.connIDToDrm[s.newConnID]
					drm.connID = s.newConnID
					go drm.mainRoutine()
					ack := NewAck(drm.connID, drm.seqNum)
					err := SendMsgToUDPWithAddr(ack, drm.conn, readMsg.addr)
					if err != nil {
						s.serverErrorInLspChannel <- err
					}
					s.newConnID++
				}
			case MsgCAck, MsgAck, MsgData:
				if connID, exists := s.hostPortToConnIDs[addrStr]; exists && msg.ConnID == connID {
					drm := s.connIDToDrm[connID]
					drm.udpToLspChannel <- msg
				}
			}
		}
	}
}
