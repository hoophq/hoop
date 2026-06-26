package apiconnections

import (
	"encoding/base64"
	"strings"
)

// Value prefixes that mark an envvar as a pointer to an external secret
// provider rather than the secret itself. Keep in sync with
// agent/secretsmanager/, agent/rds/iam_rds.go, agent/controller/agent.go.
var secretReferencePrefixes = []string{
	"_aws:",
	"_envjson:",
	"_vaultkv1:",
	"_vaultkv2:",
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

func hasAnySecretReference(envs map[string]string) bool {
	for _, v := range envs {
		if IsSecretReference(v) {
			return true
		}
	}
	return false
}

// stripInlineSecrets returns a copy of envs where inline secret values
// are blanked out. References and boolean toggles round-trip; the key
// set is preserved so the UI knows which credentials exist.
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

// mergeSecrets applies a patch onto existing envs. Patch semantics:
// non-empty value replaces (or adds), empty value deletes, absent key
// is preserved. Returns the new map and whether anything changed.
// `existing` is not mutated.
func mergeSecrets(existing, patch map[string]string) (map[string]string, bool) {
	if patch == nil {
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
