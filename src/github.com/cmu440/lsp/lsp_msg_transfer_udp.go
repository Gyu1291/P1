package lsp

import (
	"encoding/json"

	"github.com/cmu440/lspnet"
)

func recvMsgFromUDP(conn *lspnet.UDPConn) (*Message, error) {
	var receivedMsgBytes []byte
	numBytes, err := conn.Read(receivedMsgBytes)
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

func sendMsgToUDP(msg *Message, conn *lspnet.UDPConn) error {
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = conn.Write(msgBytes)
	if err != nil {
		return err
	}
	return nil
}
