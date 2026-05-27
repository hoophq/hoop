package apiconnections

import (
	"encoding/base64"
	"strings"
)

// secretReferencePrefixes lists the value prefixes that indicate an envvar
// holds a reference to an external secret provider rather than the secret
// itself. References are not sensitive (they only name where to look) and
// must remain visible to admins so they can audit and reconfigure them.
//
// Keep in sync with:
//   - agent/secretsmanager/secretsmanager.go (provider constants)
//   - agent/rds/iam_rds.go                   (_aws_iam_rds prefix)
//   - agent/controller/agent.go              (_aws_iam_rds detection)
var secretReferencePrefixes = []string{
	"_aws:",
	"_envjson:",
	"_vaultkv1:",
	"_vaultkv2:",
	"_aws_iam_rds:",
}

// IsSecretReference reports whether a stored envvar value is a reference to
// an external secret provider. Stored values are base64-encoded; we decode
// once and look at the leading prefix.
//
// A nil/empty value is treated as not-a-reference so it can be stripped or
// ignored by callers.
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

// stripInlineSecrets returns a copy of envs where inline secret values are
// blanked out. References to external providers are preserved verbatim so
// admins can still see what provider/key a connection points at.
//
// The returned map has the same key set as the input; only inline values
// are replaced with an empty string. This preserves the "keys present"
// shape that the UI relies on to render the list of credentials.
func stripInlineSecrets(envs map[string]string) map[string]string {
	if envs == nil {
		return nil
	}
	out := make(map[string]string, len(envs))
	for k, v := range envs {
		if IsSecretReference(v) {
			out[k] = v
			continue
		}
		out[k] = ""
	}
	return out
}

// envsEqual reports whether two envvar maps contain the same set of keys
// with the same (base64-encoded) values. nil and an empty map compare
// equal; this keeps the comparison robust against callers that pass one or
// the other interchangeably.
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

// mergeSecrets applies a patch onto an existing envs map and returns the
// resulting map plus a boolean indicating whether anything changed.
//
// Semantics:
//   - A key present in patch with a non-empty value replaces the existing
//     value (or adds a new key).
//   - A key present in patch with an empty value deletes the key from the
//     resulting map. This is how the write-only UI signals "delete this
//     secret" without ever sending the old value back to the server.
//   - A key absent from patch is preserved untouched.
//
// `existing` is not mutated.
func mergeSecrets(existing, patch map[string]string) (map[string]string, bool) {
	if patch == nil {
		// nil patch is a no-op; mirror the input
		out := make(map[string]string, len(existing))
		for k, v := range existing {
			out[k] = v
		}
		return out, false
	}
	out := make(map[string]string, len(existing)+len(patch))
	for k, v := range existing {
		out[k] = v
	}
	changed := false
	for k, v := range patch {
		if v == "" {
			if _, present := out[k]; present {
				delete(out, k)
				changed = true
			}
			continue
		}
		if existingVal, present := out[k]; !present || existingVal != v {
			out[k] = v
			changed = true
		}
	}
	return out, changed
}
