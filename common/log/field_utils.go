package log

import (
	"fmt"
	"strings"

	"go.uber.org/zap/zapcore"
)

// Field extraction utilities

// getStringField extracts string values from a fieldMap with fallback for multiple keys
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

// getBoolField extracts bool values from a fieldMap
func getBoolField(fields map[string]interface{}, key string) bool {
	if val, ok := fields[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

// getIntField extracts int values from a fieldMap with automatic conversion and fallback for multiple keys
func getIntField(fields map[string]interface{}, keys ...string) int {
	for _, key := range keys {
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
	}
	return 0
}

// ExtractFieldValue extracts the value from a zapcore.Field in a type-safe manner
func ExtractFieldValue(field zapcore.Field) interface{} {
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

// GetFieldStringValue extracts the string value from a zapcore.Field
func GetFieldStringValue(field zapcore.Field) string {
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

// BuildFieldMap combines stored fields and direct fields into a map
func BuildFieldMap(storedFields map[string]interface{}, fields []zapcore.Field) map[string]interface{} {
	fieldMap := make(map[string]interface{})

	for k, v := range storedFields {
		fieldMap[k] = v
	}

	for _, field := range fields {
		fieldMap[field.Key] = ExtractFieldValue(field)
	}

	return fieldMap
}

// Formatting utilities

// truncateSession truncates session IDs for cleaner display
func truncateSession(sid string) string {
	if len(sid) > 12 {
		return fmt.Sprintf("%s...%s", sid[:8], sid[len(sid)-4:])
	}
	return sid
}

// formatDuration formats durations from different field types
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

// identifyCommand identifies the command type based on the command string
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
		parts := strings.Fields(cmd)
		if len(parts) > 0 {
			return strings.Title(parts[0])
		}
		return "command"
	}
}
