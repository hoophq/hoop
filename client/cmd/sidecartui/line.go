package sidecartui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
)

// Attr is one key and value of a log record, in the order the daemon wrote it.
// Order matters for a reader: the daemon puts the subject first ("listener",
// "session") and the detail after.
type Attr struct {
	Key   string
	Value string
}

// LogRecord is one slog JSON line from the daemon's stderr.
type LogRecord struct {
	Time  time.Time
	Level string
	Msg   string
	Attrs []Attr
}

// Get returns the first attribute named key, or "".
func (r LogRecord) Get(key string) string {
	for _, a := range r.Attrs {
		if a.Key == key {
			return a.Value
		}
	}
	return ""
}

// ParseLog reads one slog JSON line. ok is false for anything else, so a
// panic trace or a library's stray print still reaches the screen as text.
func ParseLog(line []byte) (LogRecord, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return LogRecord{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return LogRecord{}, false
	}
	var rec LogRecord
	var sawMsg bool
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return LogRecord{}, false
		}
		key, _ := kt.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return LogRecord{}, false
		}
		switch key {
		case "time":
			var s string
			_ = json.Unmarshal(raw, &s)
			rec.Time, _ = time.Parse(time.RFC3339Nano, s)
		case "level":
			_ = json.Unmarshal(raw, &rec.Level)
		case "msg":
			_ = json.Unmarshal(raw, &rec.Msg)
			sawMsg = true
		default:
			rec.Attrs = appendAttr(rec.Attrs, key, raw)
		}
	}
	if !sawMsg || rec.Level == "" {
		return LogRecord{}, false
	}
	return rec, true
}

// appendAttr flattens a value: a group ({"metadata":{"a":"b"}}) becomes
// metadata.a=b, the way slog's text handler spells it.
func appendAttr(out []Attr, key string, raw json.RawMessage) []Attr {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '{' {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if _, err := dec.Token(); err == nil {
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					break
				}
				k, _ := kt.(string)
				var sub json.RawMessage
				if err := dec.Decode(&sub); err != nil {
					break
				}
				out = appendAttr(out, key+"."+k, sub)
			}
			return out
		}
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return append(out, Attr{key, s})
	}
	return append(out, Attr{key, string(raw)})
}

// ParseAudit reads one audit JSON line. It keys on the two fields every event
// carries and no log record does, so the two streams can share a pipe.
func ParseAudit(line []byte) (audit.Event, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return audit.Event{}, false
	}
	var probe struct {
		Kind      string `json:"kind"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(line, &probe) != nil || probe.Kind == "" || probe.SessionID == "" {
		return audit.Event{}, false
	}
	var ev audit.Event
	if json.Unmarshal(line, &ev) != nil {
		return audit.Event{}, false
	}
	return ev, true
}

// TextLine renders a log record as logfmt, the format slog.TextHandler
// writes: time=... level=INFO msg="..." key=value. No color, ever: text mode
// exists for terminals and files that cannot take it.
func TextLine(r LogRecord) string {
	var b strings.Builder
	if !r.Time.IsZero() {
		b.WriteString("time=")
		b.WriteString(r.Time.Format(time.RFC3339Nano))
		b.WriteByte(' ')
	}
	b.WriteString("level=")
	b.WriteString(r.Level)
	b.WriteString(" msg=")
	b.WriteString(quoteValue(r.Msg))
	for _, a := range r.Attrs {
		fmt.Fprintf(&b, " %s=%s", a.Key, quoteValue(a.Value))
	}
	return b.String()
}

func quoteValue(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\n\r\"=") {
		return fmt.Sprintf("%q", s)
	}
	return s
}
