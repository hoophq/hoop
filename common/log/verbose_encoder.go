package log

import (
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

// VerboseEncoder é um encoder customizado para formato verbose
type VerboseEncoder struct {
	cfg           zapcore.EncoderConfig
	useEmoji      bool
	useColor      bool
	sessionStarts map[string]time.Time
	storedFields  map[string]interface{}
	startTime     time.Time
}

// NewVerboseEncoder cria um encoder para formato verbose (human + timestamps)
func NewVerboseEncoder(cfg zapcore.EncoderConfig) zapcore.Encoder {
	return &VerboseEncoder{
		cfg:           cfg,
		useEmoji:      os.Getenv("NO_COLOR") == "",
		useColor:      os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb",
		sessionStarts: make(map[string]time.Time),
		storedFields:  make(map[string]interface{}),
		startTime:     time.Now(),
	}
}

func (v *VerboseEncoder) Clone() zapcore.Encoder {
	cloned := &VerboseEncoder{
		cfg:           v.cfg,
		useEmoji:      v.useEmoji,
		useColor:      v.useColor,
		sessionStarts: v.sessionStarts,
		storedFields:  make(map[string]interface{}),
		startTime:     v.startTime,
	}

	// Copia stored fields
	for k, val := range v.storedFields {
		cloned.storedFields[k] = val
	}

	return cloned
}

// Implementar métodos necessários do zapcore.Encoder para VerboseEncoder
func (v *VerboseEncoder) AddArray(key string, marshaler zapcore.ArrayMarshaler) error {
	return nil
}

func (v *VerboseEncoder) AddObject(key string, marshaler zapcore.ObjectMarshaler) error {
	return nil
}

func (v *VerboseEncoder) AddBinary(key string, value []byte) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddByteString(key string, value []byte) {
	v.storedFields[key] = string(value)
}

func (v *VerboseEncoder) AddBool(key string, value bool) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddComplex128(key string, value complex128) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddComplex64(key string, value complex64) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddDuration(key string, value time.Duration) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddFloat64(key string, value float64) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddFloat32(key string, value float32) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddInt(key string, value int) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddInt64(key string, value int64) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddInt32(key string, value int32) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddInt16(key string, value int16) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddInt8(key string, value int8) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddString(key, value string) {
	v.storedFields[key] = value
	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: VerboseEncoder.AddString called: %s = %s\n", key, value)
	}
}

func (v *VerboseEncoder) AddTime(key string, value time.Time) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddUint(key string, value uint) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddUint64(key string, value uint64) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddUint32(key string, value uint32) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddUint16(key string, value uint16) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddUint8(key string, value uint8) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddUintptr(key string, value uintptr) {
	v.storedFields[key] = value
}

func (v *VerboseEncoder) AddReflected(key string, value interface{}) error {
	v.storedFields[key] = value
	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: VerboseEncoder.AddReflected called: %s = %v\n", key, value)
	}
	return nil
}

func (v *VerboseEncoder) OpenNamespace(key string) {
}

func (v *VerboseEncoder) EncodeEntry(entry zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	// Formata mensagem primeiro para verificar se deve ser suprimida
	msg := v.formatMessage(entry.Message, fields)

	// Se a mensagem for vazia (suprimida), retorna buffer vazio válido
	if msg == "" {
		// Retorna um buffer válido mas vazio
		line := buffer.NewPool().Get()
		return line, nil
	}

	line := buffer.NewPool().Get()

	// Timestamp sempre visível em verbose
	line.AppendString("[")
	line.AppendTime(entry.Time, "15:04:05.000")
	line.AppendString("] ")

	// Emoji ou prefix baseado no level (só para níveis que têm emoji)
	emoji := levelEmojis[entry.Level]
	if emoji != "" && v.useEmoji {
		line.AppendString(emoji + " ")
	}

	// Mensagem principal formatada
	if v.useColor {
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

	if v.useColor {
		line.AppendString(colorReset)
	}

	// Adiciona caller info se disponível (detalhes extras em verbose)
	if entry.Caller.Defined {
		line.AppendString(" [")
		line.AppendString(entry.Caller.TrimmedPath())
		line.AppendString("]")
	}

	// Nova linha
	line.AppendString("\n")

	return line, nil
}

func (v *VerboseEncoder) formatMessage(msg string, fields []zapcore.Field) string {
	// Debug
	if os.Getenv("DEBUG_ENCODER") == "true" {
		fmt.Fprintf(os.Stderr, "DEBUG: VerboseEncoder.formatMessage called with %d direct fields, %d stored fields\n", len(fields), len(v.storedFields))
		for i, field := range fields {
			fmt.Fprintf(os.Stderr, "  direct[%d] %s = %v\n", i, field.Key, v.getFieldStringValue(field))
		}
		for k, val := range v.storedFields {
			fmt.Fprintf(os.Stderr, "  stored[%s] = %v\n", k, val)
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
			fullSessionID = v.getFieldStringValue(field)
		case "version":
			version = v.getFieldStringValue(field)
		case "platform":
			platform = v.getFieldStringValue(field)
		}
	}

	// Se não encontrou nos fields diretos, usa stored fields
	if fullSessionID == "" {
		if sid, ok := v.storedFields["sid"]; ok {
			fullSessionID = fmt.Sprintf("%v", sid)
		}
	}
	if version == "" {
		if v, ok := v.storedFields["version"]; ok {
			version = fmt.Sprintf("%v", v)
		}
	}
	if platform == "" {
		if p, ok := v.storedFields["platform"]; ok {
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

	// Usa a mesma lógica do HumanEncoder
	switch {
	case strings.Contains(msgLower, "starting agent"):
		versionInfo := ""
		if version != "" && platform != "" {
			versionInfo = fmt.Sprintf(" v%s • %s", version, platform)
		} else if version != "" {
			versionInfo = fmt.Sprintf(" v%s", version)
		}
		if v.useEmoji {
			return fmt.Sprintf("%s Starting Hoop Agent%s", emojiRocket, versionInfo)
		}
		return fmt.Sprintf("Starting Hoop Agent%s", versionInfo)

	case strings.Contains(msgLower, "connecting to") && strings.Contains(msgLower, "tls="):
		server := extractServer(msg)
		if strings.Contains(msgLower, "tls=true") {
			if v.useEmoji {
				return fmt.Sprintf("%s Connecting to %s %s", emojiLink, server, emojiLock)
			}
			return fmt.Sprintf("Connecting to %s [TLS]", server)
		} else {
			if v.useEmoji {
				return fmt.Sprintf("%s Connecting to %s %s", emojiLink, server, emojiUnlock)
			}
			return fmt.Sprintf("Connecting to %s [No TLS]", server)
		}

	case strings.Contains(msgLower, "connected with success"):
		if v.useEmoji {
			return emojiCheck + " Connected to gateway"
		}
		return "Connected successfully"

	case msgLower == "received connect request":
		// Marca o início da sessão para calcular duração
		if fullSessionID != "" {
			v.sessionStarts[fullSessionID] = time.Now()
		}
		if v.useEmoji {
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

			if v.useEmoji {
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
			if v.useEmoji {
				return prefix + emojiCheck + " Success"
			}
			return prefix + "Command completed successfully"
		} else {
			result := ""
			if v.useEmoji {
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
			if startTime, ok := v.sessionStarts[fullSessionID]; ok {
				dur := time.Since(startTime)
				duration = fmt.Sprintf(" • duration: %s", formatDuration(dur))
				delete(v.sessionStarts, fullSessionID) // Limpa do mapa
			}
		}

		if v.useEmoji {
			return fmt.Sprintf("%s🔚 Session closed%s", prefix, duration)
		}
		return fmt.Sprintf("%sSession closed%s", prefix, duration)

	case strings.Contains(msgLower, "disconnected from"):
		if strings.Contains(msgLower, "reason=") {
			// Extrai a razão
			if idx := strings.Index(msg, "reason="); idx >= 0 {
				reason := msg[idx+7:]
				if v.useEmoji {
					return fmt.Sprintf("⚠️  Disconnected: %s", reason)
				}
				return fmt.Sprintf("Disconnected: %s", reason)
			}
		}
		return msg

	case strings.Contains(msgLower, "shutting down"):
		if v.useEmoji {
			return "👋 Shutting down"
		}
		return "Shutting down agent"
	}

	// Mensagem padrão com prefixo de session se houver
	return prefix + msg
}

func (v *VerboseEncoder) getFieldStringValue(field zapcore.Field) string {
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
