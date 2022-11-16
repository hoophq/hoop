package proto

import (
	"bufio"
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"io"
	reflect "reflect"
)

type (
	ClientTransport interface {
		Send(*Packet) error
		Recv() (*Packet, error)
		StreamContext() context.Context
		StartKeepAlive()
		Close() (error, error)
	}
	PacketType        string
	ProtocolType      string
	ConnectionWrapper struct {
		conn  io.WriteCloser
		doneC chan struct{}
	}
	streamWriter struct {
		client     ClientTransport
		packetType PacketType
		packetSpec map[string][]byte
	}
	AgentConnectionParams struct {
		EnvVars      map[string]any
		CmdList      []string
		ClientArgs   []string
		DLPInfoTypes []string
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

func NewStreamWriter(client ClientTransport, packetType PacketType, spec map[string][]byte) io.WriteCloser {
	return &streamWriter{client: client, packetType: packetType, packetSpec: spec}
}

func (s *streamWriter) Write(data []byte) (int, error) {
	p := &Packet{Spec: map[string][]byte{}}
	if s.packetType == "" {
		return 0, fmt.Errorf("packet type must not be empty")
	}
	p.Type = s.packetType.String()
	p.Spec = s.packetSpec
	p.Payload = data
	return len(data), s.client.Send(p)
}

func (s *streamWriter) Close() error {
	_, _ = s.client.Close()
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

func IsInList(item string, items []string) bool {
	for _, i := range items {
		if i == item {
			return true
		}
	}
	return false
}
