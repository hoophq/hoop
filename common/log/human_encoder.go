package log

import (
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

// HumanEncoder is a custom encoder for human-readable format
type HumanEncoder struct {
	*BaseEncoder  // Composition - inherits all Add* methods
	cfg           zapcore.EncoderConfig
	sessionStarts map[string]time.Time // For tracking session durations
}

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[36m"
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
)

// Use centralized emoji constants
var levelEmojis = LevelEmojis()

// NewHumanEncoder creates an encoder for human-readable format
func NewHumanEncoder(cfg zapcore.EncoderConfig) zapcore.Encoder {
	return &HumanEncoder{
		BaseEncoder:   NewBaseEncoder(),
		cfg:           cfg,
		sessionStarts: make(map[string]time.Time),
	}
}

func (h *HumanEncoder) Clone() zapcore.Encoder {
	cloned := &HumanEncoder{
		BaseEncoder:   &BaseEncoder{},
		cfg:           h.cfg,
		sessionStarts: h.sessionStarts, // Share the map
	}

	// Copy fields using BaseEncoder method
	cloned.SetStoredFields(h.CopyStoredFields())

	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: HumanEncoder.Clone() called. Original: %p, Cloned: %p, Fields: %v\n", h, cloned, cloned.GetStoredFields())
	}

	return cloned
}

func (h *HumanEncoder) EncodeEntry(entry zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: EncodeEntry called:\n")
		fmt.Fprintf(os.Stderr, "  - Message: '%s'\n", entry.Message)
		fmt.Fprintf(os.Stderr, "  - Level: %s\n", entry.Level)
		fmt.Fprintf(os.Stderr, "  - Fields count: %d\n", len(fields))
		for i, field := range fields {
			fmt.Fprintf(os.Stderr, "    [%d] Key: '%s', Type: %v\n", i, field.Key, field.Type)
			if field.Type == zapcore.StringType {
				fmt.Fprintf(os.Stderr, "        String value: '%s'\n", field.String)
			}
			if field.Interface != nil {
				fmt.Fprintf(os.Stderr, "        Interface value: '%v'\n", field.Interface)
			}
		}
	}

	msg := h.formatMessage(entry.Message, fields)

	if msg == "" {
		line := buffer.NewPool().Get()
		return line, nil
	}

	line := buffer.NewPool().Get()

	// Always use emoji for human-readable format
	emoji := levelEmojis[entry.Level]
	if emoji != "" {
		line.AppendString(emoji + " ")
	}

	// Always use color for human-readable format
	switch entry.Level {
	case zapcore.ErrorLevel, zapcore.FatalLevel:
		line.AppendString(colorRed)
	case zapcore.WarnLevel:
		line.AppendString(colorYellow)
	case zapcore.InfoLevel:
	case zapcore.DebugLevel:
		line.AppendString(colorGray)
	}

	line.AppendString(msg)

	// Always add color reset for human-readable format
	line.AppendString(colorReset)

	line.AppendString("\n")

	return line, nil
}

func (h *HumanEncoder) formatMessage(msg string, fields []zapcore.Field) string {
	if os.Getenv("DEBUG_ENCODER") == "true" {
		storedFields := h.GetStoredFields()
		fmt.Fprintf(os.Stderr, "DEBUG: formatMessage called with %d direct fields, %d stored fields\n", len(fields), len(storedFields))
		for i, field := range fields {
			fmt.Fprintf(os.Stderr, "  direct[%d] %s = %v\n", i, field.Key, GetFieldStringValue(field))
		}
		for k, v := range storedFields {
			fmt.Fprintf(os.Stderr, "  stored[%s] = %v\n", k, v)
		}
	}

	fieldMap := BuildFieldMap(h.GetStoredFields(), fields)
	// Always use emoji for human-readable format
	return encoderUtils.FormatMessage(msg, fieldMap, true)
}

func extractServer(msg string) string {
	parts := strings.Split(msg, " ")
	for i, part := range parts {
		if part == "to" && i+1 < len(parts) {
			server := parts[i+1]
			server = strings.TrimSuffix(server, ",")
			return server
		}
	}
	return "server"
}
