package proto

import (
	"bufio"
	"bytes"
	"encoding/gob"
	"fmt"
	"io"
	reflect "reflect"
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
	AgentConnectionParams struct {
		EnvVars    map[string]interface{}
		CmdList    []string
		ClientArgs []string
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

// Deprecated: use GobDecodeInto
func GobDecodeMap(data []byte) (map[string]any, error) {
	res := map[string]any{}
	if data == nil || string(data) == "" {
		return res, nil
	}
	return res, gob.NewDecoder(bytes.NewBuffer(data)).
		Decode(&res)
}

// Deprecated: use GobEncode
func GobEncodeMap(data map[string]any) ([]byte, error) {
	buf := &bytes.Buffer{}
	err := gob.NewEncoder(buf).Encode(data)
	return buf.Bytes(), err
}

func GobEncode(data any) ([]byte, error) {
	buf := &bytes.Buffer{}
	err := gob.NewEncoder(buf).Encode(data)
	return buf.Bytes(), err
}

func GobDecodeInto(data []byte, into any) error {
	if data == nil || string(data) == "" {
		return fmt.Errorf("nothing to decode")
	}
	if reflect.ValueOf(into).Kind() != reflect.Ptr {
		return fmt.Errorf("the decoded object must be a pointer")
	}
	return gob.NewDecoder(bytes.NewBuffer(data)).Decode(into)
}

func BufferedPayload(payload []byte) *bufio.Reader {
	return bufio.NewReaderSize(bytes.NewBuffer(payload), len(payload))
}
