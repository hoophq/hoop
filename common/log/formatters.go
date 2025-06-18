package log

import "fmt"

// AgentStartFormatter handles agent startup events
type AgentStartFormatter struct{}

func (f *AgentStartFormatter) FormatHuman(fields map[string]interface{}, msg string) string {
	version := getStringField(fields, "version")
	platform := getStringField(fields, "platform")
	mode := getStringField(fields, "mode")

	if version != "" && platform != "" {
		if mode != "" {
			return fmt.Sprintf("%s Starting Hoop Agent v%s (%s) • mode: %s", EmojiRocket, version, platform, mode)
		}
		return fmt.Sprintf("%s Starting Hoop Agent v%s (%s)", EmojiRocket, version, platform)
	}
	if version != "" {
		return fmt.Sprintf("%s Starting Hoop Agent v%s", EmojiRocket, version)
	}

	// Fallback para mensagem original
	return EmojiRocket + " " + msg
}

func (f *AgentStartFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	return f.FormatHuman(fields, msg)
}

// SessionStartFormatter handles session start events
type SessionStartFormatter struct{}

func (f *SessionStartFormatter) FormatHuman(fields map[string]interface{}, msg string) string {
	sid := getStringField(fields, "sid", "session_id")
	version := getStringField(fields, "version")
	platform := getStringField(fields, "platform")

	// Para starting session, mostra o ID completo no final
	if version != "" && platform != "" {
		if sid != "" {
			return fmt.Sprintf("%s Starting session • Hoop v%s (%s) • session: %s", EmojiSession, version, platform, sid)
		}
		return fmt.Sprintf("%s Starting session • Hoop v%s (%s)", EmojiSession, version, platform)
	}
	if version != "" {
		if sid != "" {
			return fmt.Sprintf("%s Starting session • Hoop v%s • session: %s", EmojiSession, version, sid)
		}
		return fmt.Sprintf("%s Starting session • Hoop v%s", EmojiSession, version)
	}
	if sid != "" {
		return fmt.Sprintf("%s New session: %s", EmojiSession, sid)
	}

	// Fallback para mensagem original
	return EmojiSession + " " + msg
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

	result := fmt.Sprintf("%s%s Session closed", prefix, EmojiEnd)
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

		return fmt.Sprintf("%s%s Executing %s: %s%s", prefix, EmojiCommand, cmdType, displayCmd, inputInfo)
	}

	// Fallback
	return prefix + EmojiCommand + " " + msg
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
		return fmt.Sprintf("%s%s Success", prefix, EmojiSuccess)
	}

	result := fmt.Sprintf("%s%s Command failed (exit code: %d)", prefix, EmojiFailed, exitCode)
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
