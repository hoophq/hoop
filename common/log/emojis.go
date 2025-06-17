package log

import "go.uber.org/zap/zapcore"

// Centralized emoji constants for consistent usage across all logging systems
// This ensures that both common/log and libhoop/llog use the same emojis

// Level-based emojis (for log levels)
const (
	EmojiDebug = "🔍"
	EmojiWarn  = "⚠️"
	EmojiError = "❌"
	EmojiFatal = "💀"
)

// Action-based emojis (for different types of events)
const (
	// Session & Startup
	EmojiRocket   = "🚀"  // Starting agent/service
	EmojiSession  = "📦"  // Session start/management
	EmojiShutdown = "👋"  // Shutting down, goodbye
	EmojiEnd      = "⏹️" // Session end, completion

	// Connections & Network
	EmojiLink      = "🔗" // Connecting, links
	EmojiConnected = "✅" // Success, connected
	EmojiProxy     = "🌐" // Proxy, network
	EmojiReconnect = "🔄" // Reconnecting, retry

	// Commands & Actions
	EmojiCommand = "📋" // Executing commands
	EmojiSuccess = "✅" // Success, completion
	EmojiFailed  = "❌" // Failed, error

	// Security & Authentication
	EmojiLock   = "🔒" // Security, authentication, DLP
	EmojiUnlock = "🔓" // Unsecured, no encryption

	// Status indicators
	EmojiCheck = "✅" // Positive status
	EmojiCross = "❌" // Negative status
)

// AllEmojis returns a slice of all emojis used in the logging system
// Useful for removeEmojis functions
func AllEmojis() []string {
	return []string{
		// Level emojis
		EmojiDebug, EmojiWarn, EmojiError, EmojiFatal,
		// Action emojis
		EmojiRocket, EmojiSession, EmojiShutdown, EmojiEnd,
		EmojiLink, EmojiConnected, EmojiProxy, EmojiReconnect,
		EmojiCommand, EmojiSuccess, EmojiFailed,
		EmojiLock, EmojiUnlock,
		EmojiCheck, EmojiCross,
	}
}

// LevelEmojis returns a map of log levels to their corresponding emojis
func LevelEmojis() map[zapcore.Level]string {
	return map[zapcore.Level]string{
		zapcore.DebugLevel: EmojiDebug,
		zapcore.InfoLevel:  "", // No emoji for info level
		zapcore.WarnLevel:  EmojiWarn,
		zapcore.ErrorLevel: EmojiError,
		zapcore.FatalLevel: EmojiFatal,
	}
}
