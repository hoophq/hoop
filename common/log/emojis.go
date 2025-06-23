package log

import "go.uber.org/zap/zapcore"

// Centralized emoji constants for consistent usage across all logging systems
// Using expressive emojis for better visual feedback and user experience

// Level-based emojis (for log levels)
const (
	EmojiDebug = "🔍"  // Debug level indicator - magnifying glass
	EmojiWarn  = "⚠️" // Warning level indicator - warning sign
	EmojiError = "❌"  // Error level indicator - cross mark
	EmojiFatal = "💀"  // Fatal level indicator - skull
)

// Action-based emojis (for different types of events)
const (
	// Session & Startup
	EmojiRocket   = "🚀" // Starting agent/service - rocket
	EmojiSession  = "📦" // Session start/management - package/box
	EmojiShutdown = "👋" // Shutting down, goodbye - waving hand
	EmojiEnd      = "🔚" // Session end, completion - end arrow

	// Connections & Network
	EmojiLink      = "🔗" // Links, references - link symbol
	EmojiConnect   = "📡" // Connecting attempt - electric plug
	EmojiConnected = "✅" // Success, connected - check mark
	EmojiProxy     = "🌐" // Proxy, network - globe
	EmojiReconnect = "🔄" // Reconnecting, retry - counterclockwise arrows

	// Commands & Actions
	EmojiCommand = "📋" // Executing commands - clipboard
	EmojiSuccess = "✅" // Success, completion - check mark
	EmojiFailed  = "❌" // Failed, error - cross mark

	// Security & Authentication
	EmojiLock   = "🔒" // Security, authentication, DLP - locked padlock
	EmojiUnlock = "🔓" // Unsecured, no encryption - unlocked padlock

	// Status indicators
	EmojiCheck = "✅" // Positive status - check mark
	EmojiCross = "❌" // Negative status - cross mark
)

// AllEmojis returns a slice of all emojis used in the logging system
// Useful for removeEmojis functions
func AllEmojis() []string {
	return []string{
		// Level emojis
		EmojiDebug, EmojiWarn, EmojiError, EmojiFatal,
		// Action emojis
		EmojiRocket, EmojiSession, EmojiShutdown, EmojiEnd,
		EmojiLink, EmojiConnect, EmojiConnected, EmojiProxy, EmojiReconnect,
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
