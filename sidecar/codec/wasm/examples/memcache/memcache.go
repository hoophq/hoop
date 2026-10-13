package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	codec "github.com/hoophq/hoop/sidecar/codec/wasm/sdk/tinygo"
)

var crlf = []byte("\r\n")

// maxDataBytes bounds a data block to memcached's own item size ceiling
// (the -I option tops out at 1024m). It also keeps size arithmetic inside
// a 32-bit int, which is what wasm32 gives this code.
const maxDataBytes = 1 << 30

// memcache decodes one connection. The protocol is stateless at the frame
// level (every command line says how many data bytes follow it), so the
// only per-connection state is the option Open read.
type memcache struct {
	maxLineBytes int
}

// command is what the decoder knows about one memcached command word.
type command struct {
	op codec.Operation
	// access is how the command touches its keys; empty when it names none.
	access codec.Access
	// keys is where the keys sit: none, the first argument, or every
	// argument (get, gets).
	keys keyShape
	// fixed is the argument count memcached requires. A line short of it
	// gets ERROR from the server and executes nothing, so the decoder
	// reports it as other.
	fixed int
	// noreply says the command accepts a trailing noreply token.
	noreply bool
	// data says a data block of args[3]+2 bytes follows the line.
	data bool
}

type keyShape int

const (
	noKey keyShape = iota
	firstKey
	allKeys
)

// commands is the memcached text command set, keyed by the word as the
// client writes it: memcached matches case-sensitively, so GET is an
// unknown command and lands in other.
var commands = map[string]command{
	"get":  {op: codec.OpSelect, access: codec.AccessRead, keys: allKeys, fixed: 1},
	"gets": {op: codec.OpSelect, access: codec.AccessRead, keys: allKeys, fixed: 1},

	"set":     {op: codec.OpUpdate, access: codec.AccessWrite, keys: firstKey, fixed: 4, noreply: true, data: true},
	"add":     {op: codec.OpUpdate, access: codec.AccessWrite, keys: firstKey, fixed: 4, noreply: true, data: true},
	"replace": {op: codec.OpUpdate, access: codec.AccessWrite, keys: firstKey, fixed: 4, noreply: true, data: true},
	"append":  {op: codec.OpUpdate, access: codec.AccessWrite, keys: firstKey, fixed: 4, noreply: true, data: true},
	"prepend": {op: codec.OpUpdate, access: codec.AccessWrite, keys: firstKey, fixed: 4, noreply: true, data: true},
	"cas":     {op: codec.OpUpdate, access: codec.AccessWrite, keys: firstKey, fixed: 5, noreply: true, data: true},

	"incr":  {op: codec.OpUpdate, access: codec.AccessWrite, keys: firstKey, fixed: 2, noreply: true},
	"decr":  {op: codec.OpUpdate, access: codec.AccessWrite, keys: firstKey, fixed: 2, noreply: true},
	"touch": {op: codec.OpUpdate, access: codec.AccessWrite, keys: firstKey, fixed: 2, noreply: true},

	"delete":    {op: codec.OpDelete, access: codec.AccessWrite, keys: firstKey, fixed: 1, noreply: true},
	"flush_all": {op: codec.OpTruncate, noreply: true},
	"stats":     {op: codec.OpShow},
	"version":   {op: codec.OpShow},
	"verbosity": {op: codec.OpSet, fixed: 1, noreply: true},
	"quit":      {op: codec.OpOther},
}

func (m *memcache) Open(options map[string]string) error {
	m.maxLineBytes = defaultMaxLineBytes
	if raw, set := options[optMaxLineBytes]; set {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return fmt.Errorf("x-memcache: %s must be a positive integer, got %q", optMaxLineBytes, raw)
		}
		m.maxLineBytes = n
	}
	return nil
}

func (m *memcache) Decode(dir codec.Direction, data []byte) (codec.Decoded, error) {
	var out codec.Decoded
	for out.Consumed < len(data) {
		rest := data[out.Consumed:]
		line, ok, err := m.line(rest)
		if err != nil {
			return codec.Decoded{}, err
		}
		if !ok {
			break
		}
		var stmt codec.Statement
		var size int
		if dir == codec.Client {
			stmt, size, ok, err = request(line, rest)
		} else {
			stmt, size, ok, err = response(line, rest)
		}
		if err != nil {
			return codec.Decoded{}, err
		}
		if !ok {
			break
		}
		out.Statements = append(out.Statements, stmt)
		out.Consumed += size
	}
	return out, nil
}

// line splits the first CRLF-terminated line off data; ok is false while
// the terminator has not arrived. The scan stops at maxLineBytes+2, so a
// data block behind the line costs nothing, and a line past the limit
// fails at the first byte that proves it is too long: a whole replay and
// a byte-at-a-time replay then fail on the same line.
func (m *memcache) line(data []byte) (line []byte, ok bool, err error) {
	window := data
	if len(window) > m.maxLineBytes+2 {
		window = window[:m.maxLineBytes+2]
	}
	if i := bytes.Index(window, crlf); i >= 0 {
		return data[:i], true, nil
	}
	n := len(data)
	// A trailing CR may be the first byte of the terminator, so it does
	// not count against the line yet.
	if n > m.maxLineBytes+1 || (n == m.maxLineBytes+1 && data[n-1] != '\r') {
		return nil, false, fmt.Errorf("x-memcache: line longer than %d bytes without CRLF", m.maxLineBytes)
	}
	return nil, false, nil
}

// request decodes one client line, and the data block behind it when the
// command carries one. ok is false while the data block is incomplete.
func request(line, data []byte) (stmt codec.Statement, size int, ok bool, err error) {
	size = len(line) + 2
	stmt = codec.Statement{Text: string(line), Operation: codec.OpOther}
	tokens := fields(stmt.Text)
	if len(tokens) == 0 {
		return stmt, size, true, nil
	}
	word := tokens[0]
	args := tokens[1:]
	stmt = stmt.WithMetadata(commandKey, word)
	spec, known := commands[word]
	// memcached answers ERROR to an unknown word, a known word short of
	// its arguments, and a storage line with a token past <bytes> (or
	// <cas unique>) that is not noreply. It executes nothing and reads
	// no data block for any of them, so each is one statement of
	// operation other; reading a block here would feed its bytes to the
	// parser as the next command line.
	if !known || len(args) < spec.fixed {
		return stmt, size, true, nil
	}
	if spec.data && len(args) > spec.fixed && (len(args) > spec.fixed+1 || args[spec.fixed] != "noreply") {
		return stmt, size, true, nil
	}
	if spec.data {
		n, ok := dataLength(args[3])
		if !ok {
			return codec.Statement{}, 0, false, fmt.Errorf("x-memcache: %s: data length %q is not a number", word, args[3])
		}
		if len(data) < size+n+2 {
			return codec.Statement{}, 0, false, nil
		}
		size += n + 2
	}
	stmt.Operation = spec.op
	if spec.op != codec.OpOther {
		stmt.Effects = []codec.Operation{spec.op}
	}
	if spec.noreply && len(args) > spec.fixed && args[len(args)-1] == "noreply" {
		stmt = stmt.WithMetadata(noreplyKey, "true")
	}
	var keys []string
	switch spec.keys {
	case firstKey:
		keys = args[:1]
	case allKeys:
		keys = args
	}
	if len(keys) > 0 {
		stmt = stmt.WithMetadata(keyKey, keys[0])
		for _, k := range keys {
			name := strings.ToLower(k)
			stmt.Tables = append(stmt.Tables, name)
			stmt.Relations = append(stmt.Relations, codec.Relation{Name: name, Access: spec.access})
		}
	}
	return stmt, size, true, nil
}

// response decodes one server line. A VALUE line owns the data block
// behind it and counts as one row; ok is false while that block is
// incomplete. Every other reply is one line and one statement.
func response(line, data []byte) (stmt codec.Statement, size int, ok bool, err error) {
	size = len(line) + 2
	stmt = codec.Statement{Text: string(line), Operation: codec.OpOther}
	tokens := fields(stmt.Text)
	if len(tokens) == 0 {
		return stmt, size, true, nil
	}
	stmt = stmt.WithMetadata(replyKey, tokens[0])
	if tokens[0] != "VALUE" {
		return stmt, size, true, nil
	}
	// VALUE <key> <flags> <bytes> [<cas unique>]
	if len(tokens) < 4 {
		return codec.Statement{}, 0, false, fmt.Errorf("x-memcache: VALUE line %q carries no data length", stmt.Text)
	}
	n, ok := dataLength(tokens[3])
	if !ok {
		return codec.Statement{}, 0, false, fmt.Errorf("x-memcache: VALUE: data length %q is not a number", tokens[3])
	}
	if len(data) < size+n+2 {
		return codec.Statement{}, 0, false, nil
	}
	size += n + 2
	stmt.Result = &codec.ResultDetail{RowCount: 1}
	return stmt, size, true, nil
}

// dataLength reads a <bytes> token: a decimal number up to maxDataBytes.
func dataLength(token string) (int, bool) {
	n, err := strconv.ParseUint(token, 10, 31)
	if err != nil || n > maxDataBytes {
		return 0, false
	}
	return int(n), true
}

// fields splits a line the way memcached tokenizes it: on spaces, with
// runs of spaces yielding no empty tokens. A tab is part of a token.
func fields(line string) []string {
	return strings.FieldsFunc(line, func(r rune) bool { return r == ' ' })
}

// Deny renders the message as a SERVER_ERROR reply, the frame a memcached
// client already handles. Line breaks in the message would read as more
// replies, so they become spaces.
func (m *memcache) Deny(dir codec.Direction, message string) []byte {
	oneLine := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return ' '
		}
		return r
	}, message)
	return []byte("SERVER_ERROR " + oneLine + "\r\n")
}

// Content renders a request for the AI analyzer. Replies carry nothing to
// classify. The cache key is the command and the key with digits removed,
// so user:1 and user:2 share one verdict.
func (m *memcache) Content(stmt codec.Statement) (codec.Content, bool) {
	if stmt.Direction == codec.Server {
		return codec.Content{}, false
	}
	line := strings.TrimSpace(stmt.Text)
	if line == "" {
		return codec.Content{}, false
	}
	word := stmt.Metadata[commandKey]
	key := stmt.Metadata[keyKey]
	shape := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return -1
		}
		return r
	}, key)
	return codec.Content{
		Text:     "Protocol: " + protocol + "\nCommand: " + word + "\nKey: " + key + "\n\n" + line,
		CacheKey: word + "|" + shape,
	}, true
}
