package broker

import (
	"io"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

type ConnectionCommunicator interface {
	Send(data []byte) error
	Read() (int, []byte, error)
	Close() error
	WrapToConnection() net.Conn
}

type agentCommunicator struct{ conn *websocket.Conn }

func NewAgentCommunicator(conn *websocket.Conn) ConnectionCommunicator {
	return &agentCommunicator{conn: conn}
}

func (a *agentCommunicator) Send(data []byte) error {
	return a.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (a *agentCommunicator) Close() error {
	return a.conn.Close()
}

func (a *agentCommunicator) Read() (int, []byte, error) {
	_, message, err := a.conn.ReadMessage()
	if err != nil {
		return 0, nil, err
	}
	return len(message), message, nil
}

func (a *agentCommunicator) WrapToConnection() net.Conn {
	return &WSConnWrap{a.conn}
}

type clientCommunicator struct{ conn net.Conn }

func NewClientCommunicator(conn net.Conn) ConnectionCommunicator {
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

func (c *clientCommunicator) Close() error {
	return c.conn.Close()
}

func (c *clientCommunicator) WrapToConnection() net.Conn {
	return c.conn
}

// WSConnWrap wraps a websocket.Conn to implement the net.Conn interface.
type WSConnWrap struct {
	*websocket.Conn
}

func (w *WSConnWrap) Read(b []byte) (n int, err error) {
	_, message, err := w.ReadMessage()
	if err != nil {
		return 0, err
	}
	if len(message) > len(b) {
		return 0, io.ErrShortBuffer
	}
	return copy(b, message), nil
}

func (w *WSConnWrap) Write(b []byte) (n int, err error) {
	err = w.WriteMessage(websocket.BinaryMessage, b)
	return len(b), err
}

func (w *WSConnWrap) SetDeadline(t time.Time) error {
	if err := w.Conn.SetWriteDeadline(t); err != nil {
		return err
	}
	return w.Conn.SetReadDeadline(t)
}
