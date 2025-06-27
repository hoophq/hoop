package log

import (
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

// VerboseEncoder is a custom encoder for verbose format
type VerboseEncoder struct {
	*BaseEncoder  // Composition - inherits all Add* methods
	cfg           zapcore.EncoderConfig
	sessionStarts map[string]time.Time
	startTime     time.Time
}

// NewVerboseEncoder creates an encoder for verbose format (human + timestamps)
func NewVerboseEncoder(cfg zapcore.EncoderConfig) zapcore.Encoder {
	return &VerboseEncoder{
		BaseEncoder:   NewBaseEncoder(),
		cfg:           cfg,
		sessionStarts: make(map[string]time.Time),
		startTime:     time.Now().UTC(),
	}
}

func (v *VerboseEncoder) Clone() zapcore.Encoder {
	cloned := &VerboseEncoder{
		BaseEncoder:   &BaseEncoder{},
		cfg:           v.cfg,
		sessionStarts: v.sessionStarts,
		startTime:     v.startTime,
	}

	cloned.SetStoredFields(v.CopyStoredFields())

	return cloned
}

func (v *VerboseEncoder) EncodeEntry(entry zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	msg := v.formatMessage(entry.Message, fields)

	if msg == "" {
		line := buffer.NewPool().Get()
		return line, nil
	}

	line := buffer.NewPool().Get()

	// Always use UTC time with time.TimeOnly layout
	line.AppendString("[")
	line.AppendTime(entry.Time.UTC(), time.TimeOnly)
	line.AppendString("] ")
	levelStr := strings.ToUpper(entry.Level.String())

	// Always use color for human-friendly logging
	switch entry.Level {
	case zapcore.ErrorLevel, zapcore.FatalLevel:
		line.AppendString(colorRed + levelStr + colorReset)
	case zapcore.WarnLevel:
		line.AppendString(colorYellow + levelStr + colorReset)
	case zapcore.InfoLevel:
		line.AppendString(colorBlue + levelStr + colorReset)
	case zapcore.DebugLevel:
		line.AppendString(colorGray + levelStr + colorReset)
	default:
		line.AppendString(levelStr)
	}

	line.AppendString("  ")
	line.AppendString(msg)
	line.AppendString("\n")

	return line, nil
}

func (v *VerboseEncoder) formatMessage(msg string, fields []zapcore.Field) string {
	if os.Getenv("DEBUG_ENCODER") == "true" {
		storedFields := v.GetStoredFields()
		fmt.Fprintf(os.Stderr, "DEBUG: VerboseEncoder.formatMessage called with %d direct fields, %d stored fields\n", len(fields), len(storedFields))
		for i, field := range fields {
			fmt.Fprintf(os.Stderr, "  direct[%d] %s = %v\n", i, field.Key, GetFieldStringValue(field))
		}
		for k, val := range storedFields {
			fmt.Fprintf(os.Stderr, "  stored[%s] = %v\n", k, val)
		}
	}

	fieldMap := BuildFieldMap(v.GetStoredFields(), fields)
	// Always use emoji for human-friendly logging
	return encoderUtils.FormatVerboseMessage(msg, fieldMap, true)
}
