package log

import (
	"fmt"
	"strings"

	"go.uber.org/zap/zapcore"
)

// EncoderUtils contém funções compartilhadas entre encoders
type EncoderUtils struct{}

// DetectEventType tenta auto-detectar o tipo de evento baseado na mensagem (backward compatibility)
func (u *EncoderUtils) DetectEventType(msg string, fieldMap map[string]interface{}) string {
	msgLower := strings.ToLower(msg)

	switch {
	case strings.Contains(msgLower, "starting agent"):
		return "agent.start"
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

// ExtractFieldValue extrai o valor de um field de forma type-safe
func (u *EncoderUtils) ExtractFieldValue(field zapcore.Field) interface{} {
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

// GetFieldStringValue extrai o valor string de um zapcore.Field
func (u *EncoderUtils) GetFieldStringValue(field zapcore.Field) string {
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

// RemoveEmojis remove emojis de uma string formatada
func (u *EncoderUtils) RemoveEmojis(text string) string {
	emojis := AllEmojis()

	result := text
	for _, emoji := range emojis {
		result = strings.ReplaceAll(result, emoji+" ", "")
		result = strings.ReplaceAll(result, emoji, "")
	}

	return strings.TrimSpace(result)
}

// FormatLegacyMessage formata mensagens usando o sistema antigo (fallback completo)
func (u *EncoderUtils) FormatLegacyMessage(msg string, fieldMap map[string]interface{}) string {
	sid := getStringField(fieldMap, "sid", "session_id")
	prefix := ""
	if sid != "" {
		prefix = fmt.Sprintf("[%s] ", truncateSession(sid))
	}

	return prefix + msg
}

// BuildFieldMap combina stored fields e direct fields em um mapa
func (u *EncoderUtils) BuildFieldMap(storedFields map[string]interface{}, fields []zapcore.Field) map[string]interface{} {
	fieldMap := make(map[string]interface{})

	// Adiciona stored fields primeiro
	for k, v := range storedFields {
		fieldMap[k] = v
	}

	// Adiciona direct fields (sobrescreve stored se necessário)
	for _, field := range fields {
		fieldMap[field.Key] = u.ExtractFieldValue(field)
	}

	return fieldMap
}

// FormatMessage é a lógica principal de formatação compartilhada
func (u *EncoderUtils) FormatMessage(msg string, fieldMap map[string]interface{}, useEmoji bool) string {
	// 1. Verifica se é um evento estruturado
	if eventType, ok := fieldMap["event"].(string); ok {
		if formatter, exists := Events[eventType]; exists {
			formatted := formatter.FormatHuman(fieldMap, msg)
			if useEmoji {
				return formatted
			}
			return u.RemoveEmojis(formatted)
		}
	}

	// 2. Tenta auto-detectar baseado na mensagem
	detectedEvent := u.DetectEventType(msg, fieldMap)
	if detectedEvent != "" {
		if formatter, exists := Events[detectedEvent]; exists {
			formatted := formatter.FormatHuman(fieldMap, msg)
			if useEmoji {
				return formatted
			}
			return u.RemoveEmojis(formatted)
		}
	}

	// 3. Fallback final
	return u.FormatLegacyMessage(msg, fieldMap)
}

// FormatVerboseMessage é similar ao FormatMessage mas usa FormatVerbose
func (u *EncoderUtils) FormatVerboseMessage(msg string, fieldMap map[string]interface{}, useEmoji bool) string {
	// 1. Verifica se é um evento estruturado
	if eventType, ok := fieldMap["event"].(string); ok {
		if formatter, exists := Events[eventType]; exists {
			formatted := formatter.FormatVerbose(fieldMap, msg)
			if useEmoji {
				return formatted
			}
			return u.RemoveEmojis(formatted)
		}
	}

	// 2. Tenta auto-detectar baseado na mensagem
	detectedEvent := u.DetectEventType(msg, fieldMap)
	if detectedEvent != "" {
		if formatter, exists := Events[detectedEvent]; exists {
			formatted := formatter.FormatVerbose(fieldMap, msg)
			if useEmoji {
				return formatted
			}
			return u.RemoveEmojis(formatted)
		}
	}

	// 3. Fallback final
	return u.FormatLegacyMessage(msg, fieldMap)
}

// Instância global para reutilização
var encoderUtils = &EncoderUtils{}
