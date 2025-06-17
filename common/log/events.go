package log

import (
	"fmt"
	"strings"
)

// EventFormatter defines how to format different event types
type EventFormatter interface {
	FormatHuman(fields map[string]interface{}, msg string) string
	FormatVerbose(fields map[string]interface{}, msg string) string
}

// EventRegistry holds all event formatters
type EventRegistry map[string]EventFormatter

// Global registry of event formatters
var Events = EventRegistry{
	"session.start":          &SessionStartFormatter{},
	"session.cleanup":        &SessionCleanupFormatter{},
	"connection.start":       &ConnectionFormatter{},
	"connection.established": &ConnectionEstablishedFormatter{},
	"command.exec":           &CommandFormatter{},
	"command.result":         &CommandResultFormatter{},
	"agent.shutdown":         &AgentShutdownFormatter{},
}

// Helper functions for common formatting patterns
func truncateSession(sid string) string {
	if len(sid) > 12 {
		return fmt.Sprintf("%s...%s", sid[:8], sid[len(sid)-4:])
	}
	return sid
}

func formatDuration(fields map[string]interface{}) string {
	if dur, ok := fields["duration"].(string); ok {
		return dur
	}
	if durMs, ok := fields["duration_ms"].(float64); ok {
		if durMs < 1000 {
			return fmt.Sprintf("%.0fms", durMs)
		}
		return fmt.Sprintf("%.1fs", durMs/1000)
	}
	return ""
}

func identifyCommand(cmd string) string {
	if cmd == "" {
		return "command"
	}

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
			return strings.Title(parts[0])
		}
		return "command"
	}
}

// SessionStartFormatter handles session start events
type SessionStartFormatter struct{}

func (f *SessionStartFormatter) FormatHuman(fields map[string]interface{}, msg string) string {
	sid := getStringField(fields, "sid", "session_id")
	version := getStringField(fields, "version")
	platform := getStringField(fields, "platform")

	prefix := ""
	if sid != "" {
		prefix = fmt.Sprintf("[%s] ", truncateSession(sid))
	}

	if version != "" && platform != "" {
		return fmt.Sprintf("%s %sStarting session • Hoop v%s (%s)", EmojiRocket, prefix, version, platform)
	}
	if version != "" {
		return fmt.Sprintf("%s %sStarting session • Hoop v%s", EmojiRocket, prefix, version)
	}
	if sid != "" {
		return fmt.Sprintf("%s %sStarting session", EmojiRocket, prefix)
	}

	// Fallback para mensagem original
	return EmojiRocket + " " + msg
}

func (f *SessionStartFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	return f.FormatHuman(fields, msg) // Mesmo formato para verbose
}

// SessionCleanupFormatter handles session cleanup events
type SessionCleanupFormatter struct{}

func (f *SessionCleanupFormatter) FormatHuman(fields map[string]interface{}, msg string) string {
	sid := getStringField(fields, "sid", "session_id")
	duration := formatDuration(fields)

	prefix := ""
	if sid != "" {
		prefix = fmt.Sprintf("[%s] ", truncateSession(sid))
	}

	result := fmt.Sprintf("%s %sSession closed", EmojiEnd, prefix)
	if duration != "" {
		result += fmt.Sprintf(" • duration: %s", duration)
	}

	return result
}

func (f *SessionCleanupFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	return f.FormatHuman(fields, msg)
}

// ConnectionFormatter handles connection start events
type ConnectionFormatter struct{}

func (f *ConnectionFormatter) FormatHuman(fields map[string]interface{}, msg string) string {
	server := getStringField(fields, "server", "host")
	tls := getBoolField(fields, "tls")

	if server != "" {
		tlsInfo := ""
		if tls {
			tlsInfo = " (TLS)"
		}
		return fmt.Sprintf("%s Connecting to %s%s", EmojiLink, server, tlsInfo)
	}

	// Fallback para mensagem original
	return EmojiLink + " " + msg
}

func (f *ConnectionFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	return f.FormatHuman(fields, msg)
}

// ConnectionEstablishedFormatter handles successful connections
type ConnectionEstablishedFormatter struct{}

func (f *ConnectionEstablishedFormatter) FormatHuman(fields map[string]interface{}, msg string) string {
	server := getStringField(fields, "server", "host")

	if server != "" {
		return fmt.Sprintf("%s Connected to %s", EmojiConnected, server)
	}

	return EmojiConnected + " Connection established"
}

func (f *ConnectionEstablishedFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	return f.FormatHuman(fields, msg)
}

// CommandFormatter handles command execution events
type CommandFormatter struct{}

func (f *CommandFormatter) FormatHuman(fields map[string]interface{}, msg string) string {
	cmd := getStringField(fields, "command", "cmd")
	sid := getStringField(fields, "sid", "session_id")
	tty := getBoolField(fields, "tty")
	stdinSize := getIntField(fields, "stdin_size")

	prefix := ""
	if sid != "" {
		prefix = fmt.Sprintf("[%s] ", truncateSession(sid))
	}

	if cmd != "" {
		cmdType := identifyCommand(cmd)
		displayCmd := cmd
		if len(cmd) > 50 {
			displayCmd = cmd[:47] + "..."
		}

		inputInfo := ""
		if !tty && stdinSize > 0 {
			inputInfo = fmt.Sprintf(" (%d bytes input)", stdinSize)
		}

		return fmt.Sprintf("%s %sExecuting %s: %s%s", EmojiCommand, prefix, cmdType, displayCmd, inputInfo)
	}

	// Fallback
	return EmojiCommand + " " + prefix + msg
}

func (f *CommandFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	return f.FormatHuman(fields, msg)
}

// CommandResultFormatter handles command completion events
type CommandResultFormatter struct{}

func (f *CommandResultFormatter) FormatHuman(fields map[string]interface{}, msg string) string {
	exitCode := getIntField(fields, "exit_code")
	stderr := getStringField(fields, "stderr", "error")
	sid := getStringField(fields, "sid", "session_id")

	prefix := ""
	if sid != "" {
		prefix = fmt.Sprintf("[%s] ", truncateSession(sid))
	}

	if exitCode == 0 {
		return fmt.Sprintf("%s %sSuccess", EmojiSuccess, prefix)
	}

	result := fmt.Sprintf("%s %sCommand failed (exit code: %d)", EmojiFailed, prefix, exitCode)
	if stderr != "" && stderr != "<nil>" {
		result += fmt.Sprintf("\n   └─ stderr: %s", stderr)
	}

	return result
}

func (f *CommandResultFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	return f.FormatHuman(fields, msg)
}

// AgentShutdownFormatter handles agent shutdown events
type AgentShutdownFormatter struct{}

func (f *AgentShutdownFormatter) FormatHuman(fields map[string]interface{}, msg string) string {
	return EmojiShutdown + " Shutting down agent"
}

func (f *AgentShutdownFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	return f.FormatHuman(fields, msg)
}

// Helper functions to extract typed fields safely
func getStringField(fields map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if val, ok := fields[key]; ok {
			if str, ok := val.(string); ok && str != "" {
				return str
			}
		}
	}
	return ""
}

func getBoolField(fields map[string]interface{}, key string) bool {
	if val, ok := fields[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

func getIntField(fields map[string]interface{}, key string) int {
	if val, ok := fields[key]; ok {
		switch v := val.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		}
	}
	return 0
}
