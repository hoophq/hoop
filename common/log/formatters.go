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

	return EmojiRocket + " " + msg
}

func (f *AgentStartFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	version := getStringField(fields, "version")
	platform := getStringField(fields, "platform")
	mode := getStringField(fields, "mode")
	server := getStringField(fields, "server", "host")
	tls := getBoolField(fields, "tls")

	result := "Starting Hoop Agent"

	if version != "" {
		result += fmt.Sprintf("\n           Version: %s", version)
	}
	if platform != "" {
		result += fmt.Sprintf("\n           Platform: %s", platform)
	}
	if mode != "" {
		result += fmt.Sprintf("\n           Mode: %s", mode)
	}
	if server != "" {
		result += fmt.Sprintf("\n           Server: %s", server)
	}
	tlsStatus := "disabled"
	if tls {
		tlsStatus = "enabled"
	}
	result += fmt.Sprintf("\n           TLS: %s", tlsStatus)

	return result
}

// SessionStartFormatter handles session start events
type SessionStartFormatter struct{}

func (f *SessionStartFormatter) FormatHuman(fields map[string]interface{}, msg string) string {
	sid := getStringField(fields, "sid", "session_id")
	version := getStringField(fields, "version")
	platform := getStringField(fields, "platform")

	// For starting session, show complete ID at the end
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

	return EmojiSession + " " + msg
}

func (f *SessionStartFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	sid := getStringField(fields, "sid", "session_id")
	dlpTypes := getIntField(fields, "dlp_types", "dlp_info_types")

	result := fmt.Sprintf("Session started: %s", sid)

	if dlpTypes >= 0 {
		result += fmt.Sprintf("\n           DLP info types: %d", dlpTypes)
	}

	return result
}

// SessionCleanupFormatter handles session cleanup events
type SessionCleanupFormatter struct{}

func (f *SessionCleanupFormatter) FormatHuman(fields map[string]interface{}, msg string) string {
	sid := getStringField(fields, "sid", "session_id")
	duration := formatDuration(fields)

	result := fmt.Sprintf("  │ [%s] %s Session closed", truncateSession(sid), EmojiEnd)
	if duration != "" {
		result += fmt.Sprintf(" • duration: %s", duration)
	}

	return result
}

func (f *SessionCleanupFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	sid := getStringField(fields, "sid", "session_id")
	duration := formatDuration(fields)

	result := "Session closed"
	if sid != "" {
		result = fmt.Sprintf("[%s] Session closed", truncateSession(sid))
	}

	if duration != "" {
		result += fmt.Sprintf("\n           Duration: %s", duration)
	}

	return result
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
		return fmt.Sprintf("%s Connecting to %s%s", EmojiConnect, server, tlsInfo)
	}

	return EmojiConnect + " " + msg
}

func (f *ConnectionFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	server := getStringField(fields, "server", "host")
	tls := getBoolField(fields, "tls")
	timeout := getIntField(fields, "timeout")

	result := "Connecting to server..."

	if server != "" {
		result += fmt.Sprintf("\n           Server: %s", server)
	}
	if timeout > 0 {
		result += fmt.Sprintf("\n           Timeout: %ds", timeout)
	}
	tlsStatus := "disabled"
	if tls {
		tlsStatus = "enabled"
	}
	result += fmt.Sprintf("\n           TLS: %s", tlsStatus)

	return result
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
	server := getStringField(fields, "server", "host")
	responseTime := getIntField(fields, "response_time_ms")

	result := "Connected successfully"

	if server != "" {
		result += fmt.Sprintf("\n           Server: %s", server)
	}
	if responseTime > 0 {
		result += fmt.Sprintf("\n           Response time: %dms", responseTime)
	}

	return result
}

// CommandFormatter handles command execution events
type CommandFormatter struct{}

func (f *CommandFormatter) FormatHuman(fields map[string]interface{}, msg string) string {
	cmd := getStringField(fields, "command", "cmd")
	sid := getStringField(fields, "sid", "session_id")
	tty := getBoolField(fields, "tty")
	stdinSize := getIntField(fields, "stdin_size")

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

		if sid != "" {
			return fmt.Sprintf("  │ [%s] %s Executing %s: %s%s", truncateSession(sid), EmojiCommand, cmdType, displayCmd, inputInfo)
		}
		return fmt.Sprintf("%s Executing %s: %s%s", EmojiCommand, cmdType, displayCmd, inputInfo)
	}

	if sid != "" {
		return fmt.Sprintf("  │ [%s] %s %s", truncateSession(sid), EmojiCommand, msg)
	}
	return EmojiCommand + " " + msg
}

func (f *CommandFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	cmd := getStringField(fields, "command", "cmd")
	sid := getStringField(fields, "sid", "session_id")
	tty := getBoolField(fields, "tty")
	stdinSize := getIntField(fields, "stdin_size")
	tableName := getStringField(fields, "table_name")

	result := fmt.Sprintf("Executing command: %s", cmd)
	if sid != "" {
		result = fmt.Sprintf("[%s] Executing command: %s", truncateSession(sid), cmd)
	}

	if tableName != "" {
		result += fmt.Sprintf("\n           Table name: %s", tableName)
	}
	result += fmt.Sprintf("\n           TTY: %t", tty)
	if stdinSize > 0 {
		result += fmt.Sprintf("\n           Stdin size: %d bytes", stdinSize)
	}

	return result
}

// CommandResultFormatter handles command completion events
type CommandResultFormatter struct{}

func (f *CommandResultFormatter) FormatHuman(fields map[string]interface{}, msg string) string {
	exitCode := getIntField(fields, "exit_code")
	stderr := getStringField(fields, "stderr", "error")
	sid := getStringField(fields, "sid", "session_id")

	if exitCode == 0 {
		if sid != "" {
			return fmt.Sprintf("  │ [%s] %s Success", truncateSession(sid), EmojiSuccess)
		}
		return fmt.Sprintf("%s Success", EmojiSuccess)
	}

	result := fmt.Sprintf("%s Command failed (exit code: %d)", EmojiFailed, exitCode)
	if sid != "" {
		result = fmt.Sprintf("  │ [%s] %s Command failed (exit code: %d)", truncateSession(sid), EmojiFailed, exitCode)
	}

	if stderr != "" && stderr != "<nil>" {
		result += fmt.Sprintf("\n     └─ stderr: %s", stderr)
	}

	return result
}

func (f *CommandResultFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	exitCode := getIntField(fields, "exit_code")
	sid := getStringField(fields, "sid", "session_id")
	stderrSize := getIntField(fields, "stderr_size", "stderr_bytes")
	stdoutSize := getIntField(fields, "stdout_size", "stdout_bytes")
	duration := formatDuration(fields)
	stderr := getStringField(fields, "stderr", "error")

	result := "Command completed successfully"
	if exitCode != 0 {
		result = "Command failed"
	}
	if sid != "" {
		if exitCode == 0 {
			result = fmt.Sprintf("[%s] Command completed successfully", truncateSession(sid))
		} else {
			result = fmt.Sprintf("[%s] Command failed", truncateSession(sid))
		}
	}

	result += fmt.Sprintf("\n           Exit code: %d", exitCode)

	if duration != "" {
		result += fmt.Sprintf("\n           Duration: %s", duration)
	}

	if stderrSize > 0 {
		result += fmt.Sprintf("\n           Stderr: %d bytes written", stderrSize)
	}
	if stdoutSize >= 0 {
		result += fmt.Sprintf("\n           Stdout: %d bytes written", stdoutSize)
	}

	// If there's stderr content and not just size
	if stderr != "" && stderr != "<nil>" && stderrSize == 0 {
		result += fmt.Sprintf("\n           Stderr: %s", stderr)
	}

	return result
}

// AgentShutdownFormatter handles agent shutdown events
type AgentShutdownFormatter struct{}

func (f *AgentShutdownFormatter) FormatHuman(fields map[string]interface{}, msg string) string {
	return EmojiShutdown + " Shutting down agent"
}

func (f *AgentShutdownFormatter) FormatVerbose(fields map[string]interface{}, msg string) string {
	reason := getStringField(fields, "reason")
	uptime := formatDuration(fields)

	result := "Shutting down agent"

	if reason != "" {
		result += fmt.Sprintf("\n           Reason: %s", reason)
	}
	if uptime != "" {
		result += fmt.Sprintf("\n           Uptime: %s", uptime)
	}

	return result
}
