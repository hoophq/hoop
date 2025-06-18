package log

import (
	"fmt"
	"strings"

	"go.uber.org/zap/zapcore"
)

// Field extraction utilities

// getStringField extrai valores string de um fieldMap com fallback para múltiplas chaves
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

// getBoolField extrai valores bool de um fieldMap
func getBoolField(fields map[string]interface{}, key string) bool {
	if val, ok := fields[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}

// getIntField extrai valores int de um fieldMap com conversão automática
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

// ExtractFieldValue extrai o valor de um zapcore.Field de forma type-safe
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

// GetFieldStringValue extrai o valor string de um zapcore.Field
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

// BuildFieldMap combina stored fields e direct fields em um mapa
func BuildFieldMap(storedFields map[string]interface{}, fields []zapcore.Field) map[string]interface{} {
	fieldMap := make(map[string]interface{})

	// Adiciona stored fields primeiro
	for k, v := range storedFields {
		fieldMap[k] = v
	}

	// Adiciona direct fields (sobrescreve stored se necessário)
	for _, field := range fields {
		fieldMap[field.Key] = ExtractFieldValue(field)
	}

	return fieldMap
}

// Formatting utilities

// truncateSession trunca session IDs para exibição mais limpa
func truncateSession(sid string) string {
	if len(sid) > 12 {
		return fmt.Sprintf("%s...%s", sid[:8], sid[len(sid)-4:])
	}
	return sid
}

// formatDuration formata durações de diferentes tipos de campos
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

// identifyCommand identifica o tipo de comando baseado na string do comando
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
