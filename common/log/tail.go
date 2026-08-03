package log

import (
	"time"

	"github.com/hoophq/hoop/common/memory"
	"go.uber.org/zap/zapcore"
)

// tailCapacity bounds how many recent log entries are kept in memory.
const tailCapacity = 2000

// TailEntry is one captured log record.
type TailEntry struct {
	Timestamp time.Time      `json:"timestamp"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Logger    string         `json:"logger,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
}

// Tail buffers the most recent log entries of this process. The capture
// floor follows the process log level (LOG_LEVEL / SetDefaultLoggerLevel).
var Tail = memory.NewRing[TailEntry](tailCapacity)

// tailCore is a zapcore.Core that appends every enabled entry to Tail. It
// shares the logger's atomic level, so the capture floor tracks LOG_LEVEL
// and runtime level changes.
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
	Tail.Append(TailEntry{
		Timestamp: ent.Time,
		Level:     ent.Level.String(),
		Message:   ent.Message,
		Logger:    logger,
		Fields:    fields,
	})
	return nil
}

func (c *tailCore) Sync() error { return nil }
