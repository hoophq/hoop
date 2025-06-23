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
	useEmoji      bool
	useColor      bool
	sessionStarts map[string]time.Time
	startTime     time.Time
}

// NewVerboseEncoder creates an encoder for verbose format (human + timestamps)
func NewVerboseEncoder(cfg zapcore.EncoderConfig) zapcore.Encoder {
	return &VerboseEncoder{
		BaseEncoder:   NewBaseEncoder(),
		cfg:           cfg,
		useEmoji:      os.Getenv("NO_COLOR") == "",
		useColor:      os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
		sessionStarts: make(map[string]time.Time),
		startTime:     time.Now(),
	}
}

func (v *VerboseEncoder) Clone() zapcore.Encoder {
	cloned := &VerboseEncoder{
		BaseEncoder:   &BaseEncoder{},
		cfg:           v.cfg,
		useEmoji:      v.useEmoji,
		useColor:      v.useColor,
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

	line.AppendString("[")
	line.AppendTime(entry.Time, "15:04:05")
	line.AppendString("] ")
	levelStr := strings.ToUpper(entry.Level.String())
	if v.useColor {
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
	} else {
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
	return encoderUtils.FormatVerboseMessage(msg, fieldMap, v.useEmoji)
}
