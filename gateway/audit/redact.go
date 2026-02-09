package audit

// redactKeys are request payload keys that must be redacted before storing.
var redactKeys = map[string]bool{
	"password": true, "hashed_password": true, "client_secret": true,
	"secret": true, "secrets": true, "api_key": true, "token": true, "key": true,
	"env": true, "envs": true, "rollout_api_key": true,
}

const redactedPlaceholder = "[REDACTED]"

// Redact returns a copy of m with secret-like keys replaced by [REDACTED].
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
		if sub, ok := v.(map[string]any); ok {
			out[k] = Redact(sub)
		} else {
			out[k] = v
		}
	}
	return out
}
