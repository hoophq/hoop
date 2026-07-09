package apiconnections

import (
	"encoding/base64"
	"strings"
)

// Value prefixes that mark an envvar as a pointer to an external secret
// provider rather than the secret itself. Keep in sync with
// agent/secretsmanager/, agent/rds/iam_rds.go, agent/controller/agent.go
// and webapp_v2 secretsCodec.js REFERENCE_PREFIXES.
// _aws_iam_rds is a mode marker ("mint an RDS IAM auth token at connect
// time"), not stored credentials — it must round-trip under
// hide_role_info or the UI loses the AWS IAM method detection.
var secretReferencePrefixes = []string{
	"_aws:",
	"_envjson:",
	"_vaultkv1:",
	"_vaultkv2:",
	"_aws_iam_rds:",
}

// IsSecretReference reports whether a base64-encoded envvar value decodes
// to a string starting with one of secretReferencePrefixes.
func IsSecretReference(encodedValue string) bool {
	if encodedValue == "" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(encodedValue)
	if err != nil {
		return false
	}
	plain := string(decoded)
	for _, prefix := range secretReferencePrefixes {
		if strings.HasPrefix(plain, prefix) {
			return true
		}
	}
	return false
}

// stripInlineSecrets returns a copy of envs where inline secret values
// are blanked out. Only external references round-trip — every inline
// value, including boolean toggles, is masked; the key set is preserved
// so the UI knows which credentials exist.
func stripInlineSecrets(envs map[string]string) map[string]any {
	if envs == nil {
		return nil
	}
	out := make(map[string]any, len(envs))
	for k, v := range envs {
		if IsSecretReference(v) {
			out[k] = v
			continue
		}
		out[k] = nil
	}
	return out
}

// overlaySecrets merges an incoming envvar map onto the stored one,
// preserving existing values wherever the incoming value is empty or
// the key is absent. It is used when an organization has hide_role_info
// enabled: the API masks envvar values on read, so a full-replace (PUT)
// payload arrives with blanked secrets that must never overwrite the
// stored credentials. Non-empty incoming values still replace (or add)
// the corresponding entry so admins can rotate a secret in place.
// `existing` is not mutated.
func overlaySecrets(existing map[string]string, incoming map[string]*string) map[string]string {
	out := make(map[string]string, len(incoming))
	for k, v := range incoming {
		if v == nil {
			out[k] = existing[k]
		} else {
			out[k] = *v
		}
	}

	return out
}

// envsEqual reports whether two envvar maps have the same keys and
// values. nil and an empty map compare equal.
func envsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if other, ok := b[k]; !ok || other != v {
			return false
		}
	}
	return true
}
