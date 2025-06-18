package log

import (
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

// HumanEncoder é um encoder customizado para formato humano
type HumanEncoder struct {
	cfg           zapcore.EncoderConfig
	useEmoji      bool
	useColor      bool
	sessionStarts map[string]time.Time   // Para rastrear duração das sessões
	storedFields  map[string]interface{} // NOVO: armazenar fields
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

// NewHumanEncoder cria um encoder para formato humano
func NewHumanEncoder(cfg zapcore.EncoderConfig) zapcore.Encoder {
	return &HumanEncoder{
		cfg:           cfg,
		useEmoji:      os.Getenv("NO_COLOR") == "",
		useColor:      os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
		sessionStarts: make(map[string]time.Time),
		storedFields:  make(map[string]interface{}),
	}
}

func (h *HumanEncoder) Clone() zapcore.Encoder {
	cloned := &HumanEncoder{
		cfg:           h.cfg,
		useEmoji:      h.useEmoji,
		useColor:      h.useColor,
		sessionStarts: h.sessionStarts,              // Compartilha o mapa
		storedFields:  make(map[string]interface{}), // Novo mapa para o clone
	}

	// Copia fields existentes
	for k, v := range h.storedFields {
		cloned.storedFields[k] = v
	}

	// Debug do clone
	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: HumanEncoder.Clone() called. Original: %p, Cloned: %p, Fields: %v\n", h, cloned, cloned.storedFields)
	}

	return cloned
}

func (h *HumanEncoder) EncodeEntry(entry zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	// Debug detalhado
	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: EncodeEntry called:\n")
		fmt.Fprintf(os.Stderr, "  - Message: '%s'\n", entry.Message)
		fmt.Fprintf(os.Stderr, "  - Level: %s\n", entry.Level)
		fmt.Fprintf(os.Stderr, "  - Fields count: %d\n", len(fields))
		for i, field := range fields {
			fmt.Fprintf(os.Stderr, "    [%d] Key: '%s', Type: %v\n", i, field.Key, field.Type)
			// Tenta extrair o valor usando diferentes métodos
			if field.Type == zapcore.StringType {
				fmt.Fprintf(os.Stderr, "        String value: '%s'\n", field.String)
			}
			if field.Interface != nil {
				fmt.Fprintf(os.Stderr, "        Interface value: '%v'\n", field.Interface)
			}
		}
	}

	// Formata mensagem primeiro para verificar se deve ser suprimida
	msg := h.formatMessage(entry.Message, fields)

	// Se a mensagem for vazia (suprimida), retorna buffer vazio
	if msg == "" {
		// Retorna um buffer válido mas vazio
		line := buffer.NewPool().Get()
		return line, nil
	}

	line := buffer.NewPool().Get()

	// Emoji ou prefix baseado no level (só para níveis que têm emoji)
	emoji := levelEmojis[entry.Level]
	if emoji != "" && h.useEmoji {
		line.AppendString(emoji + " ")
	}

	// Mensagem principal formatada
	if h.useColor {
		switch entry.Level {
		case zapcore.ErrorLevel, zapcore.FatalLevel:
			line.AppendString(colorRed)
		case zapcore.WarnLevel:
			line.AppendString(colorYellow)
		case zapcore.InfoLevel:
			// Info usa cor padrão do terminal (sem cor)
		case zapcore.DebugLevel:
			line.AppendString(colorGray)
		}
	}

	line.AppendString(msg)

	if h.useColor {
		line.AppendString(colorReset)
	}

	// Nova linha
	line.AppendString("\n")

	return line, nil
}

func (h *HumanEncoder) formatMessage(msg string, fields []zapcore.Field) string {
	// Debug
	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: formatMessage called with %d direct fields, %d stored fields\n", len(fields), len(h.storedFields))
		for i, field := range fields {
			fmt.Fprintf(os.Stderr, "  direct[%d] %s = %v\n", i, field.Key, encoderUtils.GetFieldStringValue(field))
		}
		for k, v := range h.storedFields {
			fmt.Fprintf(os.Stderr, "  stored[%s] = %v\n", k, v)
		}
	}

	// Usa a função compartilhada para construir o fieldMap
	fieldMap := encoderUtils.BuildFieldMap(h.storedFields, fields)

	// Usa a função compartilhada para formatação
	return encoderUtils.FormatMessage(msg, fieldMap, h.useEmoji)
}

func extractServer(msg string) string {
	// Extrai o servidor de mensagens como "connecting to server.com:8443, tls=true"
	parts := strings.Split(msg, " ")
	for i, part := range parts {
		if part == "to" && i+1 < len(parts) {
			server := parts[i+1]
			// Remove vírgula se houver
			server = strings.TrimSuffix(server, ",")
			return server
		}
	}
	return "server"
}

// Funções removidas - agora estão em encoder_utils.go

// Implementar métodos necessários do zapcore.Encoder
func (h *HumanEncoder) AddArray(key string, marshaler zapcore.ArrayMarshaler) error {
	return nil
}

func (h *HumanEncoder) AddObject(key string, marshaler zapcore.ObjectMarshaler) error {
	return nil
}

func (h *HumanEncoder) AddBinary(key string, value []byte) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddByteString(key string, value []byte) {
	h.storedFields[key] = string(value)
}

func (h *HumanEncoder) AddBool(key string, value bool) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddComplex128(key string, value complex128) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddComplex64(key string, value complex64) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddDuration(key string, value time.Duration) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddFloat64(key string, value float64) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddFloat32(key string, value float32) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddInt(key string, value int) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddInt64(key string, value int64) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddInt32(key string, value int32) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddInt16(key string, value int16) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddInt8(key string, value int8) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddString(key, value string) {
	h.storedFields[key] = value
	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: AddString called: %s = %s\n", key, value)
	}
}

func (h *HumanEncoder) AddTime(key string, value time.Time) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddUint(key string, value uint) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddUint64(key string, value uint64) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddUint32(key string, value uint32) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddUint16(key string, value uint16) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddUint8(key string, value uint8) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddUintptr(key string, value uintptr) {
	h.storedFields[key] = value
}

func (h *HumanEncoder) AddReflected(key string, value interface{}) error {
	h.storedFields[key] = value
	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: AddReflected called: %s = %v\n", key, value)
	}
	return nil
}

func (h *HumanEncoder) OpenNamespace(key string) {
}
