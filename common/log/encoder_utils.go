package log

import (
	"fmt"
	"strings"
)

// EncoderUtils contains shared encoding logic between encoders
type EncoderUtils struct{}

// DetectEventType attempts to auto-detect event type based on message (backward compatibility)
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

// RemoveEmojis removes emojis from a formatted string
func (u *EncoderUtils) RemoveEmojis(text string) string {
	emojis := AllEmojis()

	result := text
	for _, emoji := range emojis {
		result = strings.ReplaceAll(result, emoji+" ", "")
		result = strings.ReplaceAll(result, emoji, "")
	}

	return strings.TrimSpace(result)
}

// FormatLegacyMessage formats messages using the legacy system (complete fallback)
func (u *EncoderUtils) FormatLegacyMessage(msg string, fieldMap map[string]interface{}) string {
	sid := getStringField(fieldMap, "sid", "session_id")
	if sid != "" {
		return fmt.Sprintf("  │ [%s] %s", truncateSession(sid), msg)
	}

	return msg
}

// FormatLegacyVerboseMessage formats verbose messages without indentation (session ID only)
func (u *EncoderUtils) FormatLegacyVerboseMessage(msg string, fieldMap map[string]interface{}) string {
	sid := getStringField(fieldMap, "sid", "session_id")
	if sid != "" {
		return fmt.Sprintf("[%s] %s", truncateSession(sid), msg)
	}

	return msg
}

// FormatMessage is the main shared formatting logic
func (u *EncoderUtils) FormatMessage(msg string, fieldMap map[string]interface{}, useEmoji bool) string {
	if eventType, ok := fieldMap["event"].(string); ok {
		if formatter, exists := Events[eventType]; exists {
			formatted := formatter.FormatHuman(fieldMap, msg)
			if useEmoji {
				return formatted
			}
			return u.RemoveEmojis(formatted)
		}
	}

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

	return u.FormatLegacyMessage(msg, fieldMap)
}

// FormatVerboseMessage is similar to FormatMessage but uses FormatVerbose
func (u *EncoderUtils) FormatVerboseMessage(msg string, fieldMap map[string]interface{}, useEmoji bool) string {
	if eventType, ok := fieldMap["event"].(string); ok {
		if formatter, exists := Events[eventType]; exists {
			formatted := formatter.FormatVerbose(fieldMap, msg)
			if useEmoji {
				return formatted
			}
			return u.RemoveEmojis(formatted)
		}
	}

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

	return u.FormatLegacyVerboseMessage(msg, fieldMap)
}

// Global instance for reuse
var encoderUtils = &EncoderUtils{}
