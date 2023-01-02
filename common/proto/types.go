package proto

import (
	"bufio"
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"io"
	reflect "reflect"

	"github.com/hoophq/pluginhooks"
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
		client         ClientTransport
		packetType     PacketType
		packetSpec     map[string][]byte
		pluginHookExec PluginHookExec
	}
	AgentConnectionParams struct {
		ConnectionName string
		ConnectionType string
		UserID         string
		EnvVars        map[string]any
		CmdList        []string
		ClientArgs     []string
		DLPInfoTypes   []string
		PluginHookList []map[string]any
	}
	PluginHookExec interface {
		ExecRPCOnSend(*pluginhooks.Request) ([]byte, error)
		ExecRPCOnRecv(*pluginhooks.Request) ([]byte, error)
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

func NewStreamWriter(client ClientTransport, pktType PacketType, spec map[string][]byte) io.WriteCloser {
	return &streamWriter{client: client, packetType: pktType, packetSpec: spec}
}

func NewHookStreamWriter(
	client ClientTransport,
	pktType PacketType,
	spec map[string][]byte,
	hookExec PluginHookExec) io.WriteCloser {
	return &streamWriter{client: client, packetType: pktType, packetSpec: spec, pluginHookExec: hookExec}
}

func (s *streamWriter) Write(data []byte) (int, error) {
	if s.client == nil {
		return 0, fmt.Errorf("stream writer client is empty")
	}
	packetType := s.packetType.String()
	p := &Packet{Spec: map[string][]byte{}}
	p.Type = packetType
	p.Spec = s.packetSpec
	p.Payload = data

	if s.pluginHookExec != nil {
		mutateData, err := s.pluginHookExec.ExecRPCOnSend(&pluginhooks.Request{
			PacketType: p.Type,
			Payload:    data,
		})
		if err != nil {
			return 0, err
		}
		// mutate if the hooks returns any payload
		if len(mutateData) > 0 {
			p.Payload = mutateData
		}
	}
	return len(p.Payload), s.client.Send(p)
}

func (s *streamWriter) Close() error {
	if s.client != nil {
		_, _ = s.client.Close()
	}
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
