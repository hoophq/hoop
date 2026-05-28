package events

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ValidateParameterMapping checks that every JSONPath in mapping references a
// root field present in the schema of every selected event type.
func ValidateParameterMapping(eventTypes []string, mapping map[string]string) error {
	for paramName, jsonPath := range mapping {
		root, err := extractRootField(jsonPath)
		if err != nil {
			return fmt.Errorf("parameter %q: %w", paramName, err)
		}

		for _, et := range eventTypes {
			evType, ok := Catalog[et]
			if !ok {
				return fmt.Errorf("unknown event type %q", et)
			}
			fields := evType.SchemaFieldNames()
			if !fields[root] {
				return fmt.Errorf("parameter %q references field %q which is not in schema of event %q", paramName, root, et)
			}
		}
	}
	return nil
}

// RenderParameters applies a parameter mapping to an actual event payload,
// returning the resolved key→value pairs.
func RenderParameters(mapping map[string]string, payload json.RawMessage) (map[string]string, error) {
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal event payload: %w", err)
	}

	result := make(map[string]string, len(mapping))
	for paramName, jsonPath := range mapping {
		val, err := resolvePath(jsonPath, data)
		if err != nil {
			return nil, fmt.Errorf("parameter %q (path %q): %w", paramName, jsonPath, err)
		}
		result[paramName] = val
	}
	return result, nil
}

// extractRootField parses a simple JSONPath like "$.field" or "$.field[0]"
// and returns the root field name (the part after "$." and before any "[").
func extractRootField(path string) (string, error) {
	if !strings.HasPrefix(path, "$.") {
		return "", fmt.Errorf("jsonpath must start with %q, got %q", "$.", path)
	}
	rest := path[2:]
	if rest == "" {
		return "", fmt.Errorf("empty field name in jsonpath %q", path)
	}
	bracketIdx := strings.Index(rest, "[")
	if bracketIdx == 0 {
		return "", fmt.Errorf("missing field name before '[' in jsonpath %q", path)
	}
	if bracketIdx > 0 {
		return rest[:bracketIdx], nil
	}
	return rest, nil
}

func resolvePath(path string, data map[string]any) (string, error) {
	root, err := extractRootField(path)
	if err != nil {
		return "", err
	}

	val, ok := data[root]
	if !ok {
		return "", fmt.Errorf("field %q not found in payload", root)
	}

	rest := path[2+len(root):]
	if rest == "" {
		return stringify(val), nil
	}

	// Handle array index: [N]
	if strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") {
		idxStr := rest[1 : len(rest)-1]
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			return "", fmt.Errorf("invalid array index %q", idxStr)
		}
		arr, ok := val.([]any)
		if !ok {
			return "", fmt.Errorf("field %q is not an array", root)
		}
		if idx < 0 || idx >= len(arr) {
			return "", fmt.Errorf("index %d out of range for field %q (length %d)", idx, root, len(arr))
		}
		return stringify(arr[idx]), nil
	}

	return "", fmt.Errorf("unsupported jsonpath suffix %q", rest)
}

func stringify(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(val)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}
