package log

import (
	"fmt"
	"os"
	"path/filepath"
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

	// Extrai campos importantes dos fields diretos OU stored fields
	var sessionID string
	var fullSessionID string
	var version, platform string

	// Primeiro tenta fields diretos
	for _, field := range fields {
		switch field.Key {
		case "sid", "session_id":
			fullSessionID = h.getFieldStringValue(field)
		case "version":
			version = h.getFieldStringValue(field)
		case "platform":
			platform = h.getFieldStringValue(field)
		}
	}

	// Se não encontrou nos fields diretos, usa stored fields
	if fullSessionID == "" {
		if sid, ok := h.storedFields["sid"]; ok {
			fullSessionID = fmt.Sprintf("%v", sid)
		}
	}
	if version == "" {
		if v, ok := h.storedFields["version"]; ok {
			version = fmt.Sprintf("%v", v)
		}
	}
	if platform == "" {
		if p, ok := h.storedFields["platform"]; ok {
			platform = fmt.Sprintf("%v", p)
		}
	}

	// Processa session ID para exibição
	if fullSessionID != "" {
		if len(fullSessionID) > 12 {
			sessionID = fmt.Sprintf("[%s...%s]", fullSessionID[:8], fullSessionID[len(fullSessionID)-4:])
		} else {
			sessionID = fmt.Sprintf("[%s]", fullSessionID)
		}
	}

	// Define prefix com session ID quando disponível
	prefix := ""
	if sessionID != "" {
		prefix = sessionID + " "
	}

	// Converte mensagem para lowercase para comparação
	msgLower := strings.ToLower(msg)

	// Casos especiais de mensagens conhecidas do agent
	switch {
	case strings.Contains(msgLower, "starting agent"):
		versionInfo := ""
		if version != "" && platform != "" {
			versionInfo = fmt.Sprintf(" v%s • %s", version, platform)
		} else if version != "" {
			versionInfo = fmt.Sprintf(" v%s", version)
		}
		if h.useEmoji {
			return fmt.Sprintf("%s Starting Hoop Agent%s", emojiRocket, versionInfo)
		}
		return fmt.Sprintf("Starting Hoop Agent%s", versionInfo)

	case strings.Contains(msgLower, "connecting to") && strings.Contains(msgLower, "tls="):
		server := extractServer(msg)
		if strings.Contains(msgLower, "tls=true") {
			if h.useEmoji {
				return fmt.Sprintf("%s Connecting to %s %s", emojiLink, server, emojiLock)
			}
			return fmt.Sprintf("Connecting to %s [TLS]", server)
		} else {
			if h.useEmoji {
				return fmt.Sprintf("%s Connecting to %s %s", emojiLink, server, emojiUnlock)
			}
			return fmt.Sprintf("Connecting to %s [No TLS]", server)
		}

	case strings.Contains(msgLower, "connected with success"):
		if h.useEmoji {
			return emojiCheck + " Connected to gateway"
		}
		return "Connected successfully"

	case msgLower == "received connect request":
		// Marca o início da sessão para calcular duração
		if fullSessionID != "" {
			h.sessionStarts[fullSessionID] = time.Now()
		}
		if h.useEmoji {
			// Mostra o session ID completo na primeira vez
			return fmt.Sprintf("%s New session: %s", emojiLink, fullSessionID)
		}
		return fmt.Sprintf("New session: %s", fullSessionID)

	case msgLower == "sent gateway connect ok":
		// Suprimir - redundante com "New session"
		return ""

	case msgLower == "received execution request":
		// Suprimir - redundante com "Executing:"
		return ""

	case strings.HasPrefix(msgLower, "tty=false"):
		// Extrai informações do comando
		var stdinSize int
		if idx := strings.Index(msg, "stdinsize="); idx >= 0 {
			fmt.Sscanf(msg[idx:], "stdinsize=%d", &stdinSize)
		}

		// Extrai o comando
		if idx := strings.Index(msg, "executing command:"); idx >= 0 {
			cmd := msg[idx+18:]
			// Remove colchetes
			cmd = strings.TrimPrefix(cmd, "[")
			cmd = strings.TrimSuffix(cmd, "]")

			// Identifica o tipo de comando
			cmdType := identifyCommand(cmd)

			// Trunca comandos muito longos mas mostra o tipo
			displayCmd := cmd
			if len(cmd) > 50 {
				displayCmd = cmd[:47] + "..."
			}

			// Adiciona info de input se relevante
			inputInfo := ""
			if stdinSize > 0 {
				inputInfo = fmt.Sprintf(" (%d bytes input)", stdinSize)
			}

			if h.useEmoji {
				return fmt.Sprintf("%s%s Executing %s: %s%s", prefix, emojiCommand, cmdType, displayCmd, inputInfo)
			}
			return fmt.Sprintf("%sExecuting %s: %s%s", prefix, cmdType, displayCmd, inputInfo)
		}
		return prefix + msg

	case strings.HasPrefix(msgLower, "exitcode="):
		// Formata mensagem de saída
		var exitCode int
		var errMsg string

		// Extrai exit code e mensagem de erro
		fmt.Sscanf(msg, "exitcode=%d", &exitCode)
		if idx := strings.Index(msg, "err="); idx >= 0 {
			errMsg = strings.TrimSpace(msg[idx+4:])
		}

		if exitCode == 0 {
			if h.useEmoji {
				return prefix + emojiCheck + " Success"
			}
			return prefix + "Command completed successfully"
		} else {
			result := ""
			if h.useEmoji {
				result = fmt.Sprintf("%s⚠️  Command failed (exit code: %d)", prefix, exitCode)
			} else {
				result = fmt.Sprintf("%sCommand failed (exit code %d)", prefix, exitCode)
			}

			// Adiciona stderr se houver
			if errMsg != "" && errMsg != "<nil>" {
				result += fmt.Sprintf("\n   └─ stderr: %s", errMsg)
			}

			return result
		}

	case msgLower == "cleaning up session":
		// Calcula duração da sessão
		duration := ""
		if fullSessionID != "" {
			if startTime, ok := h.sessionStarts[fullSessionID]; ok {
				dur := time.Since(startTime)
				duration = fmt.Sprintf(" • duration: %s", formatDuration(dur))
				delete(h.sessionStarts, fullSessionID) // Limpa do mapa
			}
		}

		if h.useEmoji {
			return fmt.Sprintf("%s🔚 Session closed%s", prefix, duration)
		}
		return fmt.Sprintf("%sSession closed%s", prefix, duration)

	case strings.Contains(msgLower, "disconnected from"):
		if strings.Contains(msgLower, "reason=") {
			// Extrai a razão
			if idx := strings.Index(msg, "reason="); idx >= 0 {
				reason := msg[idx+7:]
				if h.useEmoji {
					return fmt.Sprintf("⚠️  Disconnected: %s", reason)
				}
				return fmt.Sprintf("Disconnected: %s", reason)
			}
		}
		return msg

	case strings.Contains(msgLower, "shutting down"):
		if h.useEmoji {
			return "👋 Shutting down"
		}
		return "Shutting down agent"
	}

	// Mensagem padrão com prefixo de session se houver
	return prefix + msg
}

// formatDuration formata duração para exibição humana
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

// identifyCommand identifica o tipo de comando sendo executado
func identifyCommand(cmd string) string {
	cmdLower := strings.ToLower(cmd)
	switch {
	case strings.Contains(cmdLower, "psql"):
		return "PostgreSQL"
	case strings.Contains(cmdLower, "mysql"):
		return "MySQL"
	case strings.Contains(cmdLower, "mongosh") || strings.Contains(cmdLower, "mongo"):
		return "MongoDB"
	case strings.Contains(cmdLower, "redis-cli"):
		return "Redis"
	case strings.Contains(cmdLower, "ssh"):
		return "SSH"
	case strings.Contains(cmdLower, "bash") || strings.Contains(cmdLower, "sh"):
		return "Shell"
	default:
		// Pega o primeiro comando
		parts := strings.Fields(cmd)
		if len(parts) > 0 {
			return filepath.Base(parts[0])
		}
		return "command"
	}
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
