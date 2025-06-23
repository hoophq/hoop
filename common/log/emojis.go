package log

import "go.uber.org/zap/zapcore"

// Centralized emoji constants for consistent usage across all logging systems

// Level-based emojis
const (
	EmojiDebug = "🔍"
	EmojiWarn  = "⚠️"
	EmojiError = "❌"
	EmojiFatal = "💀"
)

// Action-based emojis
const (
	// Session & Startup
	EmojiRocket   = "🚀"
	EmojiSession  = "📦"
	EmojiShutdown = "👋"
	EmojiEnd      = "🔚"

	// Connections & Network
	EmojiLink      = "🔗"
	EmojiConnect   = "📡"
	EmojiConnected = "✅"
	EmojiProxy     = "🌐"
	EmojiReconnect = "🔄"

	// Commands & Actions
	EmojiCommand = "📋"
	EmojiSuccess = "✅"
	EmojiFailed  = "❌"

	// Security & Authentication
	EmojiLock   = "🔒"
	EmojiUnlock = "🔓"

	// Status indicators
	EmojiCheck = "✅"
	EmojiCross = "❌"
)

// AllEmojis returns a slice of all emojis used in the logging system
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
