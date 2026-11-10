// Package jsonsql reads the SQL a JSON document carries: a string at a
// dotted path of a JSON object, the arguments of a JSON-RPC tools/call, one
// statement per message in a batch.
//
// It knows JSON and JSON-RPC and nothing of the protocol that carried the
// document. A protocol seam routes a message to it and turns what it returns
// into statements of that protocol: codec/http does it for request bodies
// (the http.sql block). A WebSocket message or a gRPC message rendered as
// JSON can go through the same Reader.
package jsonsql

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
)

// Options configures a Reader.
type Options struct {
	// Dialect classifies the SQL.
	Dialect lexer.Dialect

	// Tools names the JSON-RPC tools whose tools/call carries SQL. Where
	// the SQL is a tool argument (ToolArgument), a message with a "jsonrpc"
	// member carries SQL only as a tools/call naming one of these, and any
	// other message (initialize, tools/list, another tool) carries none.
	Tools []string

	// Refuse lists document values that send a request's results around the
	// relay: Databricks' disposition EXTERNAL_LINKS answers with links to
	// cloud storage, so the rows never cross the lane that masks them. A
	// document carrying one is refused, with RefuseMessage, whatever the
	// lane's rules say.
	Refuse        []RefuseValue
	RefuseMessage string
}

// RefuseValue is one Refuse entry: the JSON path and the value at it,
// compared case-insensitively.
type RefuseValue struct {
	Field string
	Value string
}

// SQL is one SQL string a document carried, or the place one should have
// been.
type SQL struct {
	// Text is the SQL, or the whole document when Unreadable is set: the
	// closest thing to the SQL there is.
	Text string

	// Analysis is the classifier's reading of Text under the dialect. It is
	// zero when Unreadable is set.
	Analysis inspect.SQLAnalysis

	// Unreadable says why no SQL could be read where the caller said it
	// would be. A seam reports such a statement as OpUnknown, with this as
	// its reason, so a rule naming `unknown` refuses it.
	Unreadable string
}

// Reader reads SQL out of JSON documents. It holds no state between
// documents, so one Reader serves every connection of a lane.
type Reader struct {
	opts Options
}

// New builds a Reader.
func New(o Options) *Reader {
	return &Reader{opts: o}
}

// Both refusals wrap inspect.ErrStreamUnsafe, which the gate denies
// regardless of policy and answers with the error text: no rule configured
// them, and none can switch them off. The text leads with what the client
// should do; the sentinel's own text follows it.

// refuseResults refuses a document whose results would not cross the relay.
func (r *Reader) refuseResults(reason string) error {
	msg := r.opts.RefuseMessage
	if msg == "" {
		msg = "this listener refuses requests whose results would not pass through it"
	}
	return fmt.Errorf("%s (%s): %w", msg, reason, inspect.ErrStreamUnsafe)
}

// Unparseable answers a document that cannot be read at all: cut at the
// caller's capture limit, or not JSON. Its SQL is unknown, and so is
// whether it asks for results that skip the relay. A Reader with Refuse
// refuses it, or padding a document past the limit would get it past
// Refuse; any other returns one Unreadable SQL for the lane's rules.
func (r *Reader) Unparseable(doc, reason string) ([]SQL, error) {
	if len(r.opts.Refuse) > 0 {
		return nil, fmt.Errorf("this listener could not read this request's body to check it: %s: %w",
			reason, inspect.ErrStreamUnsafe)
	}
	return []SQL{{Text: doc, Unreadable: reason}}, nil
}

// Read returns the SQL doc carries at field, a dotted path such as
// "statement" or "params.arguments.query", in document order. A refusal
// comes back as an error and no SQL.
func (r *Reader) Read(doc, field string) ([]SQL, error) {
	dec := json.NewDecoder(strings.NewReader(doc))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return r.Unparseable(doc, "the body is not JSON")
	}

	messages := []any{v}
	if batch, isBatch := v.([]any); isBatch {
		messages = batch
	}
	toolRoute := ToolArgument(field)
	var out []SQL
	for _, m := range messages {
		obj, isObject := m.(map[string]any)
		if !isObject {
			out = append(out, SQL{Text: doc, Unreadable: "the body holds a JSON value that is not an object"})
			continue
		}
		// Before any skip: a refused value sends the results around the
		// relay, whatever else the message holds.
		if err := r.checkRefuse(obj); err != nil {
			return nil, err
		}
		if _, rpc := obj["jsonrpc"]; rpc && toolRoute && !r.toolCall(obj) {
			continue // a JSON-RPC message that runs no SQL tool
		}
		sql, isString := Lookup(obj, field).(string)
		if !isString || strings.TrimSpace(sql) == "" {
			out = append(out, SQL{Text: doc, Unreadable: fmt.Sprintf("no SQL string at %q", field)})
			continue
		}
		out = append(out, SQL{Text: sql, Analysis: inspect.AnalyzeSQLIn(sql, r.opts.Dialect)})
	}
	return out, nil
}

// checkRefuse refuses a message holding a Refuse value.
func (r *Reader) checkRefuse(obj map[string]any) error {
	for _, v := range r.opts.Refuse {
		switch got := Lookup(obj, v.Field).(type) {
		case nil: // absent or null: the server's default
		case string:
			if strings.EqualFold(got, v.Value) {
				return r.refuseResults(fmt.Sprintf("%s is %s", v.Field, got))
			}
		default:
			// Databricks also takes an enum by its number, and
			// "disposition": 2 is EXTERNAL_LINKS. A value that is not a
			// string cannot be compared with the operator's, so it is
			// refused rather than guessed at.
			return r.refuseResults(fmt.Sprintf("%s is not a string", v.Field))
		}
	}
	return nil
}

// ToolArgument reports whether field is an argument of a JSON-RPC
// tools/call, which MCP puts under params.arguments. Only there does the
// tool a message calls decide whether it carries SQL. Anywhere else
// "jsonrpc", "method" and "params" are members the client chose:
// Databricks' Statement Execution API runs a body that also holds
// "jsonrpc": 1, so skipping on them would let its DROP past the lane.
func ToolArgument(field string) bool {
	return strings.HasPrefix(field, "params.arguments.")
}

// toolCall reports whether a JSON-RPC message calls one of the SQL tools.
func (r *Reader) toolCall(msg map[string]any) bool {
	if method, _ := msg["method"].(string); method != "tools/call" {
		return false
	}
	name, _ := Lookup(msg, "params.name").(string)
	return slices.Contains(r.opts.Tools, name)
}

// Lookup follows a dotted path through JSON objects. An absent member, or a
// step into something that is not an object, yields nil.
func Lookup(v any, path string) any {
	for _, key := range strings.Split(path, ".") {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		if v, ok = obj[key]; !ok {
			return nil
		}
	}
	return v
}

// Member reads the string member key of the JSON object doc by its exact key.
// It refuses a key that repeats, in any case, and a value with invalid UTF-8
// or a lone surrogate escape.
func Member(doc []byte, key string) (string, error) {
	notObject := errors.New("not a JSON object")
	dec := json.NewDecoder(bytes.NewReader(doc))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", notObject
	}
	var found string
	var raw json.RawMessage
	for dec.More() {
		tok, err := dec.Token()
		k, isKey := tok.(string)
		if err != nil || !isKey {
			return "", notObject
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return "", notObject
		}
		if !strings.EqualFold(k, key) {
			continue
		}
		if raw != nil {
			if found == k {
				return "", fmt.Errorf("member %q appears twice", k)
			}
			return "", fmt.Errorf("members %q and %q differ only in case", found, k)
		}
		found, raw = k, v
	}
	if _, err := dec.Token(); err != nil {
		return "", notObject
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", notObject
	}
	var s string
	switch {
	case raw == nil:
		return "", fmt.Errorf("member %q is absent", key)
	case found != key:
		return "", fmt.Errorf("member %q is absent; %q differs from it in case", key, found)
	case raw[0] != '"' || json.Unmarshal(raw, &s) != nil:
		return "", fmt.Errorf("member %q is not a string", key)
	case strings.TrimSpace(s) == "":
		return "", fmt.Errorf("member %q is blank", key)
	case !wellFormed(raw):
		return "", fmt.Errorf("member %q holds invalid UTF-8 or a lone surrogate escape", key)
	}
	return s, nil
}

// wellFormed reports whether the JSON string raw holds valid UTF-8 and no
// lone surrogate escape: the two inputs Go decodes to U+FFFD.
func wellFormed(raw []byte) bool {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		r := hexRune(raw[i+1 : i+5])
		i += 4
		if !utf16.IsSurrogate(r) {
			continue
		}
		if !bytes.HasPrefix(raw[i+1:], []byte(`\u`)) || utf16.DecodeRune(r, hexRune(raw[i+3:i+7])) == unicode.ReplacementChar {
			return false
		}
		i += 6
	}
	return true
}

func hexRune(h []byte) rune {
	n, _ := strconv.ParseUint(string(h), 16, 32)
	return rune(n)
}

// ValidPath accepts a dotted path of member names with no empty or blank
// segment, the form Lookup follows.
func ValidPath(p string) bool {
	if p == "" {
		return false
	}
	for _, seg := range strings.Split(p, ".") {
		if strings.TrimSpace(seg) == "" {
			return false
		}
	}
	return true
}
