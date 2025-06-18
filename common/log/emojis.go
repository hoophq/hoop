package log

import "go.uber.org/zap/zapcore"

// Centralized Unicode symbol constants for consistent usage across all logging systems
// Using lightweight Unicode characters instead of heavy emojis for better log performance

// Level-based symbols (for log levels)
const (
	EmojiDebug = "•" // Debug level indicator
	EmojiWarn  = "!" // Warning level indicator
	EmojiError = "×" // Error level indicator
	EmojiFatal = "※" // Fatal level indicator
)

// Action-based symbols (for different types of events)
const (
	// Session & Startup
	EmojiRocket   = "▲" // Starting agent/service
	EmojiSession  = "■" // Session start/management
	EmojiShutdown = "▼" // Shutting down, goodbye
	EmojiEnd      = "◆" // Session end, completion

	// Connections & Network
	EmojiLink      = "~" // Connecting, links
	EmojiConnected = "✓" // Success, connected
	EmojiProxy     = "○" // Proxy, network
	EmojiReconnect = "↻" // Reconnecting, retry

	// Commands & Actions
	EmojiCommand = "►" // Executing commands
	EmojiSuccess = "✓" // Success, completion
	EmojiFailed  = "×" // Failed, error

	// Security & Authentication
	EmojiLock   = "▣" // Security, authentication, DLP
	EmojiUnlock = "▢" // Unsecured, no encryption

	// Status indicators
	EmojiCheck = "✓" // Positive status
	EmojiCross = "×" // Negative status
)

// AllEmojis returns a slice of all symbols used in the logging system
// Useful for removeEmojis functions
func AllEmojis() []string {
	return []string{
		// Level symbols
		EmojiDebug, EmojiWarn, EmojiError, EmojiFatal,
		// Action symbols
		EmojiRocket, EmojiSession, EmojiShutdown, EmojiEnd,
		EmojiLink, EmojiConnected, EmojiProxy, EmojiReconnect,
		EmojiCommand, EmojiSuccess, EmojiFailed,
		EmojiLock, EmojiUnlock,
		EmojiCheck, EmojiCross,
	}
}

// LevelEmojis returns a map of log levels to their corresponding symbols
func LevelEmojis() map[zapcore.Level]string {
	return map[zapcore.Level]string{
		zapcore.DebugLevel: EmojiDebug,
		zapcore.InfoLevel:  "", // No symbol for info level
		zapcore.WarnLevel:  EmojiWarn,
		zapcore.ErrorLevel: EmojiError,
		zapcore.FatalLevel: EmojiFatal,
	}
}
