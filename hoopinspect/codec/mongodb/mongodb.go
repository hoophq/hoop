// Package mongodb decodes the MongoDB wire protocol far enough to recover the
// command a client sent.
//
// Envoy has no MongoDB SQL-equivalent inspection: `mongo_proxy` emits counters
// and can log queries, but produces no dynamic metadata a policy can act on.
//
// Wire format reference:
// https://www.mongodb.com/docs/manual/reference/mongodb-wire-protocol/
//
// Every message has a 16-byte header:
//
//	int32 messageLength (including the header)
//	int32 requestID
//	int32 responseTo
//	int32 opCode
//
// Modern drivers send OP_MSG (2013) exclusively; OP_QUERY (2004) survives only
// in the initial handshake. This decoder handles both.
//
// OP_MSG body:
//
//	uint32 flagBits
//	then one or more sections:
//	  kind 0: a single BSON document (the command)
//	  kind 1: int32 size, C-string identifier, then a BSON document sequence
//
// The command name is the FIRST key of the kind-0 document — that is how the
// server itself dispatches — and its value names the collection.
//
// This package implements just enough BSON to walk a document's top level and
// render it as JSON. It does NOT pull in a BSON library, because the whole
// module ships zero dependencies on purpose.
package mongodb

import (
	"encoding/binary"
	"errors"
	"math"
	"strconv"
	"strings"

	"github.com/hoophq/hoopinspect"
)

func init() { hoopinspect.Register(func() hoopinspect.Codec { return Codec{} }) }

// Op codes.
const (
	opQuery = 2004
	opMsg   = 2013
)

const (
	headerLen = 16

	// maxMessageLen matches the server's own 48 MB limit on a wire message.
	maxMessageLen = 48 << 20
)

// ErrMalformed means the stream is not valid MongoDB wire protocol.
var ErrMalformed = errors.New("hoopinspect/mongodb: malformed message")

// Codec implements hoopinspect.Codec for MongoDB.
type Codec struct{}

func (Codec) Protocol() hoopinspect.Protocol { return hoopinspect.MongoDB }

// writeCommands map to a write-shaped Operation; everything else that is not a
// read is classified OpOther. Keeping this explicit (rather than inferring)
// means a new server command defaults to "not obviously safe" instead of being
// silently classified as a read.
var commandOps = map[string]hoopinspect.Operation{
	"find":             hoopinspect.OpFind,
	"aggregate":        hoopinspect.OpFind,
	"count":            hoopinspect.OpFind,
	"distinct":         hoopinspect.OpFind,
	"getmore":          hoopinspect.OpFind,
	"insert":           hoopinspect.OpInsert,
	"update":           hoopinspect.OpUpdate,
	"delete":           hoopinspect.OpDelete,
	"findandmodify":    hoopinspect.OpUpdate,
	"drop":             hoopinspect.OpDrop,
	"dropdatabase":     hoopinspect.OpDrop,
	"dropindexes":      hoopinspect.OpDrop,
	"create":           hoopinspect.OpCreate,
	"createindexes":    hoopinspect.OpCreate,
	"renamecollection": hoopinspect.OpAlter,
	"collmod":          hoopinspect.OpAlter,
	"ismaster":         hoopinspect.OpAdmin,
	"hello":            hoopinspect.OpAdmin,
	"buildinfo":        hoopinspect.OpAdmin,
	"ping":             hoopinspect.OpAdmin,
	"saslstart":        hoopinspect.OpAdmin,
	"saslcontinue":     hoopinspect.OpAdmin,
	"getparameter":     hoopinspect.OpAdmin,
	"listdatabases":    hoopinspect.OpShow,
	"listcollections":  hoopinspect.OpShow,
	"listindexes":      hoopinspect.OpShow,
}

// Decode implements hoopinspect.Codec.
//
// Metadata keys:
//
//	"mongo.command"    — the command name as sent (e.g. "find", "insert")
//	"mongo.collection" — the target collection, when the command names one
//	"mongo.opcode"     — "OP_MSG" or "OP_QUERY"
func (Codec) Decode(dir hoopinspect.Direction, data []byte) ([]hoopinspect.Statement, int, error) {
	if dir != hoopinspect.FromClient {
		return nil, len(data), nil
	}

	var stmts []hoopinspect.Statement
	pos := 0

	for {
		if len(data)-pos < headerLen {
			return stmts, pos, nil
		}

		msgLen := int(int32(binary.LittleEndian.Uint32(data[pos:])))
		if msgLen < headerLen || msgLen > maxMessageLen {
			return stmts, pos, ErrMalformed
		}
		if len(data)-pos < msgLen {
			return stmts, pos, nil // partial message
		}

		opCode := int32(binary.LittleEndian.Uint32(data[pos+12:]))
		body := data[pos+headerLen : pos+msgLen]
		pos += msgLen

		var doc []byte
		var opName string
		switch opCode {
		case opMsg:
			doc = opMsgDocument(body)
			opName = "OP_MSG"
		case opQuery:
			doc = opQueryDocument(body)
			opName = "OP_QUERY"
		default:
			continue
		}
		if doc == nil {
			continue
		}

		cmd, coll, db := commandFields(doc)
		if cmd == "" {
			continue
		}

		op, known := commandOps[strings.ToLower(cmd)]
		if !known {
			op = hoopinspect.OpOther
		}

		md := map[string]string{
			"mongo.command": cmd,
			"mongo.opcode":  opName,
		}
		var tables []string
		if coll != "" {
			md["mongo.collection"] = coll
			tables = []string{strings.ToLower(coll)}
		}

		stmts = append(stmts, hoopinspect.Statement{
			Protocol:  hoopinspect.MongoDB,
			Direction: hoopinspect.FromClient,
			Text:      renderJSON(doc),
			Operation: op,
			Tables:    tables,
			Database:  db,
			Metadata:  md,
		})
	}
}

// opMsgDocument returns the kind-0 section's BSON document from an OP_MSG
// body, or nil when absent.
func opMsgDocument(body []byte) []byte {
	if len(body) < 5 {
		return nil
	}
	b := body[4:] // skip flagBits

	for len(b) > 0 {
		kind := b[0]
		b = b[1:]
		switch kind {
		case 0:
			if len(b) < 4 {
				return nil
			}
			size := int(int32(binary.LittleEndian.Uint32(b)))
			if size < 5 || size > len(b) {
				return nil
			}
			return b[:size]
		case 1:
			if len(b) < 4 {
				return nil
			}
			size := int(int32(binary.LittleEndian.Uint32(b)))
			if size < 4 || size > len(b) {
				return nil
			}
			b = b[size:] // skip the whole document sequence
		default:
			return nil
		}
	}
	return nil
}

// opQueryDocument returns the query document from an OP_QUERY body.
//
// Layout: int32 flags, C-string fullCollectionName, int32 numberToSkip,
// int32 numberToReturn, then the BSON query document.
func opQueryDocument(body []byte) []byte {
	if len(body) < 4 {
		return nil
	}
	b := body[4:]

	i := indexNUL(b)
	if i < 0 {
		return nil
	}
	b = b[i+1:]

	if len(b) < 8+5 {
		return nil
	}
	b = b[8:]

	size := int(int32(binary.LittleEndian.Uint32(b)))
	if size < 5 || size > len(b) {
		return nil
	}
	return b[:size]
}

// commandFields extracts the command name, the collection it targets, and the
// database, from a command document.
//
// The command name is the first key. Its value is the collection for
// collection-scoped commands (find, insert, ...) and a number for
// database-scoped ones (dropDatabase: 1). The database is under "$db".
func commandFields(doc []byte) (cmd, collection, database string) {
	first := true
	forEachElement(doc, func(name string, typ byte, val []byte) bool {
		if first {
			cmd = name
			first = false
			if typ == bsonString {
				collection = bsonStringValue(val)
			}
		}
		if name == "$db" && typ == bsonString {
			database = bsonStringValue(val)
		}
		return true
	})
	return cmd, collection, database
}

func indexNUL(b []byte) int {
	for i := range b {
		if b[i] == 0 {
			return i
		}
	}
	return -1
}

// --- minimal BSON reader -------------------------------------------------
//
// Only what is needed to walk a command document's top level and render it.
// Full spec: https://bsonspec.org/spec.html

const (
	bsonDouble   = 0x01
	bsonString   = 0x02
	bsonDocument = 0x03
	bsonArray    = 0x04
	bsonBinary   = 0x05
	bsonUndef    = 0x06
	bsonObjectID = 0x07
	bsonBool     = 0x08
	bsonDatetime = 0x09
	bsonNull     = 0x0A
	bsonRegex    = 0x0B
	bsonDBPtr    = 0x0C
	bsonJS       = 0x0D
	bsonSymbol   = 0x0E
	bsonJSScope  = 0x0F
	bsonInt32    = 0x10
	bsonTimestmp = 0x11
	bsonInt64    = 0x12
	bsonDecimal  = 0x13
	bsonMinKey   = 0xFF
	bsonMaxKey   = 0x7F
)

// forEachElement walks the top-level elements of a BSON document, calling fn
// with each element's name, type byte and raw value bytes. fn returns false to
// stop. Malformed input terminates the walk rather than panicking.
func forEachElement(doc []byte, fn func(name string, typ byte, val []byte) bool) {
	if len(doc) < 5 {
		return
	}
	b := doc[4 : len(doc)-1] // strip int32 length and trailing NUL

	for len(b) > 0 {
		typ := b[0]
		b = b[1:]

		i := indexNUL(b)
		if i < 0 {
			return
		}
		name := string(b[:i])
		b = b[i+1:]

		n := valueLen(typ, b)
		if n < 0 || n > len(b) {
			return
		}
		if !fn(name, typ, b[:n]) {
			return
		}
		b = b[n:]
	}
}

// valueLen returns the encoded length of a BSON value of the given type, or -1
// when it cannot be determined (unknown type or truncated buffer).
func valueLen(typ byte, b []byte) int {
	switch typ {
	case bsonNull, bsonUndef, bsonMinKey, bsonMaxKey:
		return 0
	case bsonBool:
		return 1
	case bsonInt32:
		return 4
	case bsonDouble, bsonDatetime, bsonInt64, bsonTimestmp:
		return 8
	case bsonObjectID:
		return 12
	case bsonDecimal:
		return 16
	case bsonString, bsonJS, bsonSymbol:
		if len(b) < 4 {
			return -1
		}
		return 4 + int(int32(binary.LittleEndian.Uint32(b)))
	case bsonDocument, bsonArray, bsonJSScope:
		if len(b) < 4 {
			return -1
		}
		return int(int32(binary.LittleEndian.Uint32(b)))
	case bsonBinary:
		if len(b) < 5 {
			return -1
		}
		return 5 + int(int32(binary.LittleEndian.Uint32(b)))
	case bsonRegex:
		i := indexNUL(b)
		if i < 0 {
			return -1
		}
		j := indexNUL(b[i+1:])
		if j < 0 {
			return -1
		}
		return i + 1 + j + 1
	case bsonDBPtr:
		if len(b) < 4 {
			return -1
		}
		return 4 + int(int32(binary.LittleEndian.Uint32(b))) + 12
	}
	return -1
}

// bsonStringValue decodes a BSON string value (int32 length, then bytes with a
// trailing NUL).
func bsonStringValue(val []byte) string {
	if len(val) < 5 {
		return ""
	}
	n := int(int32(binary.LittleEndian.Uint32(val)))
	if n < 1 || 4+n > len(val) {
		return ""
	}
	return string(val[4 : 4+n-1]) // drop trailing NUL
}

// renderJSON renders a BSON document as compact JSON for the Statement.Text
// field. Values whose exact form does not matter to a policy render as a type
// marker rather than their contents — a policy matches on command shape, and
// dumping every inserted document would make Text unbounded.
func renderJSON(doc []byte) string {
	var b strings.Builder
	b.WriteByte('{')
	first := true

	forEachElement(doc, func(name string, typ byte, val []byte) bool {
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteByte('"')
		b.WriteString(escapeJSON(name))
		b.WriteString(`":`)
		b.WriteString(renderValue(typ, val))
		return true
	})

	b.WriteByte('}')
	return b.String()
}

func renderValue(typ byte, val []byte) string {
	switch typ {
	case bsonString, bsonJS, bsonSymbol:
		return `"` + escapeJSON(bsonStringValue(val)) + `"`
	case bsonBool:
		if len(val) == 1 && val[0] != 0 {
			return "true"
		}
		return "false"
	case bsonInt32:
		if len(val) < 4 {
			return "null"
		}
		return strconv.Itoa(int(int32(binary.LittleEndian.Uint32(val))))
	case bsonInt64, bsonDatetime, bsonTimestmp:
		if len(val) < 8 {
			return "null"
		}
		return strconv.FormatInt(int64(binary.LittleEndian.Uint64(val)), 10)
	case bsonDouble:
		if len(val) < 8 {
			return "null"
		}
		bits := binary.LittleEndian.Uint64(val)
		return strconv.FormatFloat(math.Float64frombits(bits), 'g', -1, 64)
	case bsonNull, bsonUndef:
		return "null"
	case bsonDocument:
		return renderJSON(val)
	case bsonArray:
		return renderArray(val)
	case bsonObjectID:
		return `"<objectid>"`
	case bsonBinary:
		return `"<binary>"`
	}
	return `"<` + strconv.Itoa(int(typ)) + `>"`
}

func renderArray(doc []byte) string {
	var b strings.Builder
	b.WriteByte('[')
	first := true
	forEachElement(doc, func(_ string, typ byte, val []byte) bool {
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(renderValue(typ, val))
		return true
	})
	b.WriteByte(']')
	return b.String()
}

func escapeJSON(s string) string {
	if !strings.ContainsAny(s, "\"\\\n\r\t") {
		return s
	}
	var b strings.Builder
	for i := range len(s) {
		switch c := s[i]; c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
