package audit

import "encoding/json"

// redactKeys are request payload keys that must be redacted before storing.
var redactKeys = map[string]struct{}{
	"password": {}, "hashed_password": {}, "client_secret": {},
	"secret": {}, "secrets": {}, "api_key": {}, "token": {}, "key": {},
	"env": {}, "envs": {}, "rollout_api_key": {}, "hosts_key": {},
}

const redactedPlaceholder = "[REDACTED]"

func Redact(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if _, ok := redactKeys[k]; ok {
			out[k] = redactedPlaceholder
			continue
		}
		switch val := v.(type) {
		case map[string]any:
			out[k] = Redact(val)
		case []map[string]any:
			items := make([]map[string]any, len(val))
			for i, item := range val {
				items[i] = Redact(item)
			}
			out[k] = items
		default:
			// Try JSON round-trip to handle structs/pointers
			data, err := json.Marshal(val)
			if err == nil {
				var nested map[string]any
				if json.Unmarshal(data, &nested) == nil {
					out[k] = Redact(nested)
					continue
				}
			}
			out[k] = v
		}
	}
	return out
}
