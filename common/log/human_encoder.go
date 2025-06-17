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

// emojis para diferentes níveis
var levelEmojis = map[zapcore.Level]string{
	zapcore.DebugLevel: "🔍",
	zapcore.InfoLevel:  "", // Sem emoji para info - já usamos emojis específicos
	zapcore.WarnLevel:  "⚠️",
	zapcore.ErrorLevel: "❌",
	zapcore.FatalLevel: "💀",
}

// emojis para diferentes ações
const (
	emojiRocket  = "🚀"
	emojiLink    = "🔗"
	emojiCheck   = "✅"
	emojiCross   = "❌"
	emojiSession = "📦"
	emojiCommand = "📋"
	emojiLock    = "🔒"
	emojiUnlock  = "🔓"
	emojiBroom   = "🧹"
)

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
			line.AppendString(colorBlue)
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
			fmt.Fprintf(os.Stderr, "  direct[%d] %s = %v\n", i, field.Key, h.getFieldStringValue(field))
		}
		for k, v := range h.storedFields {
			fmt.Fprintf(os.Stderr, "  stored[%s] = %v\n", k, v)
		}
	}

	// Combina todos os fields (diretos + stored) em um mapa
	fieldMap := make(map[string]interface{})

	// Adiciona stored fields primeiro
	for k, v := range h.storedFields {
		fieldMap[k] = v
	}

	// Adiciona direct fields (sobrescreve stored se necessário)
	for _, field := range fields {
		fieldMap[field.Key] = h.extractFieldValue(field)
	}

	// Verifica se é um evento estruturado
	if eventType, ok := fieldMap["event"].(string); ok {
		if formatter, exists := Events[eventType]; exists {
			// Usa o formatter específico do evento
			formatted := formatter.FormatHuman(fieldMap, msg)
			if h.useEmoji {
				return formatted
			}
			// Remove emojis se NO_COLOR está ativo
			return h.removeEmojis(formatted)
		}
	}

	// Fallback: Tenta auto-detectar baseado na mensagem (backward compatibility)
	detectedEvent := h.detectEventType(msg, fieldMap)
	if detectedEvent != "" {
		if formatter, exists := Events[detectedEvent]; exists {
			formatted := formatter.FormatHuman(fieldMap, msg)
			if h.useEmoji {
				return formatted
			}
			return h.removeEmojis(formatted)
		}
	}

	// Fallback final: Formatação manual simples com session prefix
	return h.formatLegacyMessage(msg, fieldMap)
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

// getFieldStringValue extrai o valor string de um zapcore.Field
func (h *HumanEncoder) getFieldStringValue(field zapcore.Field) string {
	// Tenta diferentes métodos para extrair o valor
	switch field.Type {
	case zapcore.StringType:
		return field.String
	case zapcore.ByteStringType:
		if field.Interface != nil {
			if bytes, ok := field.Interface.([]byte); ok {
				return string(bytes)
			}
		}
		return field.String
	default:
		if field.Interface != nil {
			return fmt.Sprintf("%v", field.Interface)
		}
		return field.String
	}
}

// extractFieldValue extrai o valor de um field de forma type-safe
func (h *HumanEncoder) extractFieldValue(field zapcore.Field) interface{} {
	switch field.Type {
	case zapcore.StringType:
		return field.String
	case zapcore.BoolType:
		return field.Integer == 1
	case zapcore.Int64Type, zapcore.Int32Type, zapcore.Int16Type, zapcore.Int8Type:
		return field.Integer
	case zapcore.Uint64Type, zapcore.Uint32Type, zapcore.Uint16Type, zapcore.Uint8Type, zapcore.UintptrType:
		return field.Integer
	case zapcore.Float64Type, zapcore.Float32Type:
		return field.Interface
	case zapcore.ByteStringType:
		if field.Interface != nil {
			if bytes, ok := field.Interface.([]byte); ok {
				return string(bytes)
			}
		}
		return field.String
	default:
		if field.Interface != nil {
			return field.Interface
		}
		return field.String
	}
}

// detectEventType tenta auto-detectar o tipo de evento baseado na mensagem (backward compatibility)
func (h *HumanEncoder) detectEventType(msg string, fieldMap map[string]interface{}) string {
	msgLower := strings.ToLower(msg)

	switch {
	case strings.Contains(msgLower, "starting agent"):
		return "session.start"
	case strings.Contains(msgLower, "connecting to") && strings.Contains(msgLower, "tls="):
		return "connection.start"
	case strings.Contains(msgLower, "connected with success"):
		return "connection.established"
	case msgLower == "received connect request":
		return "session.start"
	case strings.HasPrefix(msgLower, "tty=false") && strings.Contains(msgLower, "executing command:"):
		return "command.exec"
	case strings.HasPrefix(msgLower, "exitcode="):
		return "command.result"
	case msgLower == "cleaning up session":
		return "session.cleanup"
	case strings.Contains(msgLower, "shutting down"):
		return "agent.shutdown"
	}

	return ""
}

// removeEmojis remove emojis de uma string formatada
func (h *HumanEncoder) removeEmojis(text string) string {
	// Lista dos emojis usados nos formatters
	emojis := []string{"🚀", "🔗", "✅", "📋", "⚠️", "🔚", "👋", "🔒", "🔓"}

	result := text
	for _, emoji := range emojis {
		result = strings.ReplaceAll(result, emoji+" ", "")
		result = strings.ReplaceAll(result, emoji, "")
	}

	return strings.TrimSpace(result)
}

// formatLegacyMessage formata mensagens usando o sistema antigo (fallback completo)
func (h *HumanEncoder) formatLegacyMessage(msg string, fieldMap map[string]interface{}) string {
	// Extrai session ID para prefixo se disponível
	sid := getStringField(fieldMap, "sid", "session_id")
	prefix := ""
	if sid != "" {
		prefix = fmt.Sprintf("[%s] ", truncateSession(sid))
	}

	// Mensagem simples com prefixo de session se houver
	return prefix + msg
}

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
