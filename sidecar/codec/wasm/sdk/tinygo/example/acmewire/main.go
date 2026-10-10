// The x-acmewire plug-in in Go: the same protocol as the Rust example
// (../../../rust/examples/acmewire), as the template for a TinyGo plug-in.
//
// Frame = opcode u8, length u32 big-endian, payload. Client: Q (SQL),
// P (purge a table), A (bearer token). Server: C (column names separated
// by 0x00), D (one row of u32-length cells), R (u32 row count), E (error
// message). 0x00 bytes between frames are keepalive padding.
//
// Build (see ../../README.md):
//
//	tinygo build -o acmewire.wasm -target wasm-unknown \
//	  -tags hoop_deny,hoop_filter,hoop_rewrite,hoop_credential,hoop_content .
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	codec "github.com/hoophq/hoop/sidecar/codec/wasm/sdk/tinygo"
)

const (
	protocol    = "x-acmewire"
	verbKey     = "x-acmewire.verb"
	tokenKey    = "x-acmewire.token"
	denyPrefix  = "ACME"
	headerBytes = 5
)

func init() {
	codec.Serve(func() codec.Codec { return &acmeWire{} }, codec.Manifest{
		Protocol:     protocol,
		Label:        "Acme Wire",
		Version:      "0.1.0",
		Capabilities: []codec.Capability{codec.CapDeny, codec.CapFilter, codec.CapRewrite, codec.CapCredential, codec.CapContent},
		SQLDialect:   "mysql",
		Instances:    "per_connection",
		Options: []codec.OptionSpec{{
			Name: "deny_prefix", Label: "Deny prefix", Type: "string", Default: denyPrefix,
			Help: "Prefix of the error message a denied client receives.",
		}},
	})
}

// main never runs under wasm-unknown; Serve is called from init.
func main() {}

type acmeWire struct {
	denyPrefix string
	filter     [2]frameCursor
	rewriteBuf []byte
	columns    []string
}

// frameCursor is where the filter is in the current frame of one
// direction, so a zero inside a payload passes and a zero between frames
// does not, under any chunking.
type frameCursor struct {
	header    []byte
	remaining int
}

type frame struct {
	opcode  byte
	payload []byte
}

func (f frame) size() int { return headerBytes + len(f.payload) }

// parseFrame splits one frame off the front of data; ok is false while
// the frame is incomplete.
func parseFrame(data []byte) (frame, bool) {
	if len(data) < headerBytes {
		return frame{}, false
	}
	n := int(binary.BigEndian.Uint32(data[1:5]))
	if len(data) < headerBytes+n {
		return frame{}, false
	}
	return frame{opcode: data[0], payload: data[headerBytes : headerBytes+n]}, true
}

func encode(opcode byte, payload []byte) []byte {
	out := make([]byte, headerBytes, headerBytes+len(payload))
	out[0] = opcode
	binary.BigEndian.PutUint32(out[1:], uint32(len(payload)))
	return append(out, payload...)
}

func known(dir codec.Direction, opcode byte) bool {
	if dir == codec.Client {
		return opcode == 'Q' || opcode == 'P' || opcode == 'A'
	}
	return opcode == 'C' || opcode == 'D' || opcode == 'R' || opcode == 'E'
}

func cells(payload []byte) ([][]byte, error) {
	var out [][]byte
	for len(payload) > 0 {
		if len(payload) < 4 {
			return nil, errors.New("x-acmewire: row cell header is truncated")
		}
		n := int(binary.BigEndian.Uint32(payload))
		if len(payload) < 4+n {
			return nil, errors.New("x-acmewire: row cell overruns its frame")
		}
		out = append(out, payload[4:4+n])
		payload = payload[4+n:]
	}
	return out, nil
}

func columnNames(payload []byte) []string {
	if len(payload) == 0 {
		return nil
	}
	parts := bytes.Split(payload, []byte{0})
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = string(p)
	}
	return names
}

// other is a statement with no verb the policy vocabulary classifies;
// the native verb goes in metadata, where a metadata rule matches it.
func other(verb, text string, result *codec.ResultDetail) codec.Statement {
	return codec.Statement{Operation: codec.OpOther, Text: text, Result: result}.WithMetadata(verbKey, verb)
}

func (a *acmeWire) Open(options map[string]string) error {
	a.denyPrefix = options["deny_prefix"]
	if _, set := options["deny_prefix"]; !set {
		a.denyPrefix = denyPrefix
	}
	if a.denyPrefix == "" {
		return errors.New("x-acmewire: deny_prefix must not be empty")
	}
	return nil
}

func (a *acmeWire) Decode(dir codec.Direction, data []byte) (codec.Decoded, error) {
	var out codec.Decoded
	for out.Consumed < len(data) {
		rest := data[out.Consumed:]
		if !known(dir, rest[0]) {
			return codec.Decoded{}, fmt.Errorf("x-acmewire: unknown opcode %#x from the %s", rest[0], dir)
		}
		f, ok := parseFrame(rest)
		if !ok {
			break
		}
		var stmts []codec.Statement
		switch f.opcode {
		case 'Q':
			sql := string(f.payload)
			parts := codec.SplitSQL(sql)
			if len(parts) == 0 {
				parts = []string{sql}
			}
			for _, part := range parts {
				stmt := codec.Statement{Text: part}.WithAnalysis(codec.AnalyzeSQL(part)).WithMetadata(verbKey, "QUERY")
				stmts = append(stmts, stmt)
			}
		case 'P':
			name := string(f.payload)
			table := strings.ToLower(name)
			stmt := codec.Statement{
				Operation: codec.OpDelete, Text: "PURGE " + name,
				Effects:   []codec.Operation{codec.OpDelete},
				Relations: []codec.Relation{{Name: table, Access: codec.AccessWrite}},
				Tables:    []string{table},
			}
			stmts = []codec.Statement{stmt.WithMetadata(verbKey, "PURGE")}
		case 'A':
			stmts = []codec.Statement{other("AUTH", "AUTH", nil).WithMetadata(tokenKey, string(f.payload))}
		case 'C':
			var cols []codec.Column
			for _, name := range columnNames(f.payload) {
				cols = append(cols, codec.Column{Name: name})
			}
			stmts = []codec.Statement{other("COLUMNS", "COLUMNS", &codec.ResultDetail{Columns: cols})}
		case 'D':
			if _, err := cells(f.payload); err != nil {
				return codec.Decoded{}, err
			}
			stmts = []codec.Statement{other("ROW", "ROW", &codec.ResultDetail{RowCount: 1})}
		case 'R':
			if len(f.payload) != 4 {
				return codec.Decoded{}, errors.New("x-acmewire: R frame carries no u32 row count")
			}
			stmts = []codec.Statement{other("DONE", "DONE", &codec.ResultDetail{RowCount: int(binary.BigEndian.Uint32(f.payload))})}
		case 'E':
			stmts = []codec.Statement{other("ERROR", "ERROR: "+string(f.payload), nil)}
		}
		out.Statements = append(out.Statements, stmts...)
		out.Consumed += f.size()
	}
	return out, nil
}

func (a *acmeWire) Deny(dir codec.Direction, message string) []byte {
	return encode('E', []byte(a.denyPrefix+": "+message))
}

func (a *acmeWire) Filter(dir codec.Direction, data []byte) []byte {
	c := &a.filter[0]
	if dir == codec.Server {
		c = &a.filter[1]
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); {
		if c.remaining > 0 {
			n := min(c.remaining, len(data)-i)
			out = append(out, data[i:i+n]...)
			c.remaining -= n
			i += n
			continue
		}
		if len(c.header) == 0 && data[i] == 0 {
			i++
			continue
		}
		c.header = append(c.header, data[i])
		i++
		if len(c.header) == headerBytes {
			c.remaining = int(binary.BigEndian.Uint32(c.header[1:]))
			out = append(out, c.header...)
			c.header = c.header[:0]
		}
	}
	return out
}

func (a *acmeWire) EnableRewrite() {}

// Rewrite rebuilds every complete D frame with masked cells and forwards
// the rest untouched; a frame split across chunks waits for its tail.
func (a *acmeWire) Rewrite(data []byte, mask codec.MaskFunc) (codec.Rewritten, error) {
	a.rewriteBuf = append(a.rewriteBuf, data...)
	var out codec.Rewritten
	consumed := 0
	for {
		f, ok := parseFrame(a.rewriteBuf[consumed:])
		if !ok {
			break
		}
		raw := a.rewriteBuf[consumed : consumed+f.size()]
		switch f.opcode {
		case 'C':
			a.columns = columnNames(f.payload)
			out.Bytes = append(out.Bytes, raw...)
		case 'D':
			cs, err := cells(f.payload)
			if err != nil {
				return codec.Rewritten{}, err
			}
			// Only a cell mask changed counts, and only a row with one:
			// that is what the audit trail reports as masked.
			payload := make([]byte, 0, len(f.payload))
			changed := 0
			for i, cell := range cs {
				column := ""
				if i < len(a.columns) {
					column = a.columns[i]
				}
				masked := mask(column, cell)
				if !bytes.Equal(masked, cell) {
					changed++
				}
				payload = binary.BigEndian.AppendUint32(payload, uint32(len(masked)))
				payload = append(payload, masked...)
			}
			out.Cells += changed
			if changed > 0 {
				out.Rows++
			}
			out.Bytes = append(out.Bytes, encode('D', payload)...)
		default:
			out.Bytes = append(out.Bytes, raw...)
		}
		consumed += f.size()
	}
	a.rewriteBuf = append(a.rewriteBuf[:0], a.rewriteBuf[consumed:]...)
	return out, nil
}

// Flush drops an incomplete fragment: forwarding it would leak the cells
// mask never saw, and the connection is ending.
func (a *acmeWire) Flush(mask codec.MaskFunc) (codec.Rewritten, error) {
	a.rewriteBuf = a.rewriteBuf[:0]
	return codec.Rewritten{}, nil
}

func (a *acmeWire) TakeCredential(stmt codec.Statement) (string, codec.Statement, bool) {
	token, ok := stmt.Metadata[tokenKey]
	if !ok {
		return "", stmt, false
	}
	scrubbed := make(map[string]string, len(stmt.Metadata))
	for k, v := range stmt.Metadata {
		if k != tokenKey {
			scrubbed[k] = v
		}
	}
	stmt.Metadata = scrubbed
	return token, stmt, true
}

func (a *acmeWire) Content(stmt codec.Statement) (codec.Content, bool) {
	if stmt.Direction == codec.Server {
		return codec.Content{}, false
	}
	text := strings.TrimSpace(stmt.Text)
	if text == "" {
		return codec.Content{}, false
	}
	verb := stmt.Metadata[verbKey]
	shape := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return -1
		}
		return r
	}, strings.ToLower(text))
	return codec.Content{
		Text:     fmt.Sprintf("Protocol: %s\nVerb: %s\n\n%s", protocol, verb, text),
		CacheKey: verb + "|" + shape,
	}, true
}
