package apiconnections

import (
	"encoding/base64"
	"strings"

	"github.com/hoophq/hoop/gateway/models"
)

// Value prefixes that mark an envvar as a pointer to an external secret
// provider rather than the secret itself. Keep in sync with
// agent/secretsmanager/, agent/rds/iam_rds.go, agent/controller/agent.go.
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

// isBooleanValue reports whether an encoded envvar value decodes to
// "true" or "false". Boolean toggles aren't secrets and round-trip so
// the UI can render their current state.
func isBooleanValue(encodedValue string) bool {
	if encodedValue == "" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(encodedValue)
	if err != nil {
		return false
	}
	switch string(decoded) {
	case "true", "false":
		return true
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

// shouldRoundTripSecrets returns true when the connection's envvars
// should be returned verbatim instead of going through the write-only
// strip. Two paths:
//
//  1. Any env value is a provider reference — the connection runs
//     under Secrets Manager / AWS IAM Role and stored values are
//     pointers/config, not raw secrets. Trade-off: adding even one
//     reference to a previously-Manual connection makes the other
//     inline values visible on subsequent reads. The admin opted in.
//
//  2. The connection shape has a bespoke React renderer
//     (application/ssh, httpproxy/*, custom/{empty|linux-vm|kubernetes-token})
//     that needs the values to populate host/URL/header inputs.
//
// Everything else (catalog databases, catalog custom subtypes,
// application/{git,github,tcp}) stays write-only.
func shouldRoundTripSecrets(conn *models.Connection) bool {
	if conn == nil {
		return false
	}
	if hasAnySecretReference(conn.Envs) {
		return true
	}
	sub := conn.SubType.String
	switch conn.Type {
	case "httpproxy":
		return true
	case "application":
		return sub == "ssh"
	case "custom":
		return sub == "" || sub == "linux-vm" || sub == "kubernetes-token"
	}
	return false
}

// stripInlineSecrets returns a copy of envs where inline secret values
// are blanked out. References and boolean toggles round-trip; the key
// set is preserved so the UI knows which credentials exist.
func stripInlineSecrets(envs map[string]string) map[string]string {
	if envs == nil {
		return nil
	}
	out := make(map[string]string, len(envs))
	for k, v := range envs {
		if IsSecretReference(v) || isBooleanValue(v) {
			out[k] = v
			continue
		}
		out[k] = ""
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
