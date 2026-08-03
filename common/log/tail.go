package log

import (
	"sync"
	"time"

	"go.uber.org/zap/zapcore"
)

// TailEntry is one captured log record.
type TailEntry struct {
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Logger    string         `json:"logger,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// tailObservers holds the capture subscribers: at most one per role, added
// at boot (the gateway registers its server-logs ring, the agent its ship
// buffer; standalone registers both). When empty (CLI and any process that
// never registers), capture costs one RLock per log call and nothing else.
var (
	tailMu        sync.RWMutex
	tailObservers []func(TailEntry)
)

// TailObserve registers fn to receive every log entry this process captures
// (level per LOG_LEVEL / SetDefaultLoggerLevel).
func TailObserve(fn func(TailEntry)) {
	tailMu.Lock()
	defer tailMu.Unlock()
	tailObservers = append(tailObservers, fn)
}

// tailCore is a zapcore.Core that forwards every enabled entry to the
// registered tail observers. It shares the logger's atomic level, so the
// capture floor tracks LOG_LEVEL and runtime level changes.
type tailCore struct {
	zapcore.LevelEnabler
	fields []zapcore.Field
}

func newTailCore(enab zapcore.LevelEnabler) zapcore.Core {
	return &tailCore{LevelEnabler: enab}
}

func (c *tailCore) With(fs []zapcore.Field) zapcore.Core {
	clone := &tailCore{LevelEnabler: c.LevelEnabler}
	clone.fields = make([]zapcore.Field, 0, len(c.fields)+len(fs))
	clone.fields = append(clone.fields, c.fields...)
	clone.fields = append(clone.fields, fs...)
	return clone
}

func (c *tailCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *tailCore) Write(ent zapcore.Entry, fs []zapcore.Field) error {
	tailMu.RLock()
	obs := tailObservers
	tailMu.RUnlock()
	if len(obs) == 0 {
		return nil
	}
	var fields map[string]any
	if len(c.fields)+len(fs) > 0 {
		enc := zapcore.NewMapObjectEncoder()
		for _, f := range c.fields {
			f.AddTo(enc)
		}
		for _, f := range fs {
			f.AddTo(enc)
		}
		fields = enc.Fields
	}
	var logger string
	if ent.Caller.Defined {
		logger = ent.Caller.TrimmedPath()
	}
	entry := TailEntry{
		Timestamp: ent.Time,
		Level:     ent.Level.String(),
		Message:   ent.Message,
		Logger:    logger,
		Fields:    fields,
	}
	for _, fn := range obs {
		fn(entry)
	}
	return nil
}

func (c *tailCore) Sync() error { return nil }
