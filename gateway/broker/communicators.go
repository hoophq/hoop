package broker

import (
	"net"

	"github.com/gorilla/websocket"
)

type ConnectionCommunicator interface {
	Send(data []byte) error
	Read() (int, []byte, error)
	Close()
}

type agentCommunicator struct{ conn *websocket.Conn }

func NewAgentCommunicator(conn *websocket.Conn) *agentCommunicator {
	return &agentCommunicator{conn: conn}
}

func (a *agentCommunicator) Send(data []byte) error {
	return a.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (a *agentCommunicator) Close() {
	a.conn.Close()
}

func (a *agentCommunicator) Read() (int, []byte, error) {
	_, message, err := a.conn.ReadMessage()
	if err != nil {
		return 0, nil, err
	}
	return len(message), message, nil
}

type clientCommunicator struct{ conn net.Conn }

func NewClientCommunicator(conn net.Conn) *clientCommunicator {
	return &clientCommunicator{conn: conn}
}

func (c *clientCommunicator) Read() (int, []byte, error) {
	buffer := make([]byte, 16*1024)
	n, err := c.conn.Read(buffer)
	if err != nil {
		return 0, nil, err
	}
	return n, buffer, nil
}

func (c *clientCommunicator) Send(data []byte) error {
	_, err := c.conn.Write(data)
	return err
}

func (c *clientCommunicator) Close() {
	c.conn.Close()
}
