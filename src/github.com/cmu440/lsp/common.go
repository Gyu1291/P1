// Contains some common things used by both client and server
package lsp

import "github.com/cmu440/lspnet"

type QueryType int

const (
	Read QueryType = iota
	Write
	Timer
	Receive
	CloseConn
	Close
)

type Query struct {
	Type QueryType
	addr *lspnet.UDPAddr
	msg  *Message
}

type ConnectionInfo struct {
	status int // 0: online, 1: closed but pending, 2: closed by timeout, 3: closed and read

	id   int            // connection id
	addr lspnet.UDPAddr // address of peer

	lastRecvEpoch int // epoch of most recently received message
	lastSendEpoch int // epoch of most recently sent message

	sendBase   int                  // sequence number of first message in window
	nextSeqnum int                  // sequence number of next message to send
	lastSeqnum int                  // sequence number of last message to send
	sendBuf    map[int]*SendMessage // send buffer

	recvBase int              // sequence number to receive next
	readBase int              // sequence number to read next
	recvBuf  map[int]*Message // receive buffer
}

type SendMessage struct {
	retransmitEpoch int // epoch to retransmit message; -1 if not sent yet
	currentBackoff  int // current backoff value
	msg             *Message
}

func nextBackoff(currentBackoff int, maxBackoff int) int {
	if currentBackoff == 0 {
		return 1
	} else if currentBackoff < maxBackoff {
		return currentBackoff * 2
	}
	return maxBackoff
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
