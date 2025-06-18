package log

import (
	"fmt"
	"strings"
)

// EncoderUtils contém a lógica de encoding compartilhada entre encoders
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
