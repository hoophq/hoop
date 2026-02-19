package audit

import "encoding/json"

// redactKeys are request payload keys that must be redacted before storing.
var redactKeys = map[string]bool{
	"password": true, "hashed_password": true, "client_secret": true,
	"secret": true, "secrets": true, "api_key": true, "token": true, "key": true,
	"env": true, "envs": true, "rollout_api_key": true, "hosts_key": true,
}

const redactedPlaceholder = "[REDACTED]"

func Redact(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if redactKeys[k] {
			out[k] = redactedPlaceholder
			continue
		}
		switch val := v.(type) {
		case map[string]any:
			out[k] = Redact(val)
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
