package lsp

import (
	"encoding/json"

	"github.com/cmu440/lspnet"
)

func recvMsgFromUDP(conn *lspnet.UDPConn) (*Message, error) {
	var receivedMsgBytes []byte
	numBytes, err := conn.Read(&receivedMsgBytes)
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

func recvMsgWithAddrFromUDP(conn *lspnet.UDPConn) (*Message, *lspnet.UDPAddr, error) {
	var receivedMsgBytes []byte
	receivedMsgBytes = make([]byte, 0, 100)
	numBytes, addr, err := conn.ReadFromUDP(&receivedMsgBytes)
	if err != nil {
		return nil, nil, err
	}
	receivedMsg := &Message{}
	err = json.Unmarshal(receivedMsgBytes[:numBytes], receivedMsg)
	if err != nil {
		return nil, nil, err
	}
	return receivedMsg, addr, nil
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

func sendMsgToUDPWithAddr(msg *Message, conn *lspnet.UDPConn, addr *lspnet.UDPAddr) error {
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = conn.WriteToUDP(msgBytes, addr)
	if err != nil {
		return err
	}
	return nil
}
