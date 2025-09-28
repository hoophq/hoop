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

type AgentCommunicator struct{ conn *websocket.Conn }
type ClientCommunicator struct{ conn net.Conn }

func NewClientCommunicator(conn net.Conn) ConnectionCommunicator { // return interface
	return &ClientCommunicator{conn: conn}
}
func NewAgentCommunicator(conn *websocket.Conn) ConnectionCommunicator {
	return &AgentCommunicator{conn: conn}
}

func (a *AgentCommunicator) Send(data []byte) error {
	return a.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (a *AgentCommunicator) Close() {
	a.conn.Close()
}

func (a *AgentCommunicator) Read() (int, []byte, error) {
	_, message, err := a.conn.ReadMessage()
	if err != nil {
		return 0, nil, err
	}
	return len(message), message, nil
}

func (c *ClientCommunicator) Read() (int, []byte, error) {
	buffer := make([]byte, 16*1024)
	n, err := c.conn.Read(buffer)
	if err != nil {
		return 0, nil, err
	}
	return n, buffer, nil
}

func (c *ClientCommunicator) Send(data []byte) error {
	_, err := c.conn.Write(data)
	return err
}

func (c *ClientCommunicator) Close() {
	c.conn.Close()
}
