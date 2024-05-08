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
	ClientReceiver interface {
		Recv() (*Packet, error)
	}
	ClientTransport interface {
		ClientReceiver
		Send(*Packet) error
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
		ClientVerb     string
		DLPInfoTypes   []string
		PluginHookList []map[string]any
	}
	PluginHookExec interface {
		ExecRPCOnSend(*pluginhooks.Request) ([]byte, error)
		ExecRPCOnRecv(*pluginhooks.Request) ([]byte, error)
	}

	WriterWithSummary interface {
		WriteWithSummary(data []byte, ts *TransformationSummary) (int, error)
	}

	TransformationSummary struct {
		Index int
		Err   error
		// [info-type, transformed-bytes]
		Summary []string
		// [[count, code, details] ...]
		SummaryResult [][]string
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

func NewStdoutStreamWriter(client ClientTransport, pktType PacketType, spec map[string][]byte) io.WriteCloser {
	return &streamWriter{client: client, packetType: pktType, packetSpec: spec}
}

func NewStderrStreamWriter(client ClientTransport, pktType PacketType, spec map[string][]byte) io.WriteCloser {
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
		if p.Spec == nil {
			return 0, fmt.Errorf("packet spec is empty")
		}
		mutateData, err := s.pluginHookExec.ExecRPCOnSend(&pluginhooks.Request{
			SessionID:  string(p.Spec[SpecGatewaySessionID]),
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

func (s *streamWriter) WriteWithSummary(data []byte, ts *TransformationSummary) (int, error) {
	if s.client == nil {
		return 0, fmt.Errorf("stream writer client is empty")
	}
	p := &Packet{Spec: s.packetSpec, Type: s.packetType.String(), Payload: data}
	if tsEnc, _ := GobEncode([]*TransformationSummary{ts}); tsEnc != nil {
		p.Spec[SpecDLPTransformationSummary] = tsEnc
	}
	return len(p.Payload), s.client.Send(p)
}

func (s *streamWriter) Close() error {
	if s.client != nil {
		_, _ = s.client.Close()
	}
	return nil
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

func (t *TransformationSummary) String() string {
	if len(t.Summary) == 2 {
		return fmt.Sprintf("chunk:%v, infotype:%v, transformedbytes:%v, result:%v",
			t.Index, t.Summary[0], t.Summary[1], t.SummaryResult)
	}
	if t.Err != nil {
		return fmt.Sprintf("chunk:%v, err:%v", t.Index, t.Err)
	}
	return ""
}

// ToProtoConnectionType parse the connection type and subtype into proto type.
// These constants should be used by clients (proxy or agent)
// that will know how to deal with the underline protocol.
//
// It defaults returning the connectionType if a combination doesn't match
// to maintain compatibility with old types enums in the database
func ToConnectionType(connectionType, subtype string) ConnectionType {
	switch connectionType {
	case "application":
		if subtype == "tcp" {
			return ConnectionType(ConnectionTypeTCP)
		}
		return ConnectionType(ConnectionTypeCommandLine)
	case "custom":
		return ConnectionType(ConnectionTypeCommandLine)
	case "database":
		switch subtype {
		case "postgres":
			return ConnectionType(ConnectionTypePostgres)
		case "mysql":
			return ConnectionType(ConnectionTypeMySQL)
		case "mongodb":
			return ConnectionType(ConnectionTypeMongoDB)
		case "mssql":
			return ConnectionType(ConnectionTypeMSSQL)
		}
	}
	return ConnectionType(connectionType)
}
