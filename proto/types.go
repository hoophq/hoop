package proto

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"fmt"
	"io"
)

type (
	PacketType        string
	ProtocolType      string
	ConnectionWrapper struct {
		conn  io.WriteCloser
		doneC chan struct{}
	}
	streamWriter struct {
		streamSendFn func(p *Packet) error
		packetType   PacketType
		packetSpec   map[string][]byte
	}
)

func (t PacketType) String() string {
	return string(t)
}

// NewConnectionWrapper initializes a new connection wrapper
func NewConnectionWrapper(conn io.WriteCloser, doneC chan struct{}) *ConnectionWrapper {
	return &ConnectionWrapper{doneC: doneC, conn: conn}
}

func (c *ConnectionWrapper) Close() error {
	go func() {
		select {
		case <-c.doneC:
			return
		default:
			close(c.doneC)
		}
	}()
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *ConnectionWrapper) Write(p []byte) (int, error) {
	return c.conn.Write(p)
}

// TODO: must be writer closer???
func NewStreamWriter(sendFn func(*Packet) error, packetType PacketType, spec map[string][]byte) io.WriteCloser {
	return &streamWriter{streamSendFn: sendFn, packetType: packetType, packetSpec: spec}
}

func (s *streamWriter) Write(data []byte) (int, error) {
	p := &Packet{Spec: map[string][]byte{}}
	if s.packetType == "" {
		return 0, fmt.Errorf("packet type must not be empty")
	}
	if s.streamSendFn == nil {
		return 0, fmt.Errorf("send func must not empty")
	}
	p.Type = s.packetType.String()
	p.Spec = s.packetSpec
	p.Payload = data
	return len(data), s.streamSendFn(p)
}

func (s *streamWriter) Close() error {
	return nil
}

func GobDecodeMap(data []byte) (map[string]any, error) {
	res := map[string]any{}
	if data == nil || string(data) == "" {
		return res, nil
	}
	return res, gob.NewDecoder(bytes.NewBuffer(data)).
		Decode(&res)
}

func GobEncodeMap(data map[string]any) ([]byte, error) {
	buf := &bytes.Buffer{}
	err := gob.NewEncoder(buf).Encode(data)
	return buf.Bytes(), err
}

func BufferedPayload(payload []byte) *bufio.Reader {
	return bufio.NewReaderSize(bytes.NewBuffer(payload), len(payload))
}
