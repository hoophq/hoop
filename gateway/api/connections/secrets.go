package apiconnections

import (
	"encoding/base64"
	"strings"

	"github.com/hoophq/hoop/gateway/models"
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

// isBooleanValue reports whether an encoded envvar value decodes to exactly
// "true" or "false". Boolean configuration flags (e.g. envvar:INSECURE,
// envvar:SSLMODE-ish toggles) aren't secrets — they're settings that
// influence connection behaviour and the UI needs to display their current
// state so toggles work. We exempt them from stripping.
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

// freeFormCustomSubtypes lists the `custom` subtypes whose envvars are
// user data rather than predefined credentials. Mirrors the dispatch in
// CLJS webapp/.../configure_role/credentials_tab.cljs:14-20 — the same
// subtypes that fall through to server/credentials-step (free-form)
// instead of metadata-driven (catalog). Empty subtype also counts.
var freeFormCustomSubtypes = map[string]bool{
	"tcp":         true,
	"httpproxy":   true,
	"ssh":         true,
	"linux-vm":    true,
	"claude-code": true,
}

// shouldRoundTripSecrets reports whether a connection's envvars are
// safe to send back to the admin UI verbatim instead of going through
// the write-only strip.
//
// Two cases bypass the strip:
//
//  1. Free-form custom envvars (any `type=custom` connection) — user
//     data, not credentials.
//
//  2. Predefined renderers that the v2 React form currently relies on
//     to populate visible fields (Anthropic URL, Kubernetes cluster
//     URL, SSH host, etc). Without these values round-tripping, the
//     custom-credential renderers in
//     webapp_v2/.../Configure/components/CredentialsTab.jsx cannot
//     show the existing configuration on edit. CLJS already round-
//     trips for these — keeping write-only here would silently drop
//     features compared to the legacy form.
//
// Catalog **database** connections stay write-only: host/user/password
// are pure credentials, the write-only model is correct, and the
// React `database-catalog` renderer handles the "Set" / "Replace"
// pattern already.
//
// Edge cases live in `/configure-role-gaps.md` (G1, G2, …) — keep
// that doc in sync when this list changes.
func shouldRoundTripSecrets(conn *models.Connection) bool {
	if conn == nil {
		return false
	}
	sub := conn.SubType.String
	switch conn.Type {
	case "custom":
		// Free-form OR any predefined-custom subtype (kubernetes-token,
		// dynamodb, aws-cloudwatch, legacy cloudwatch, …). The React
		// dispatch in CredentialsTab.jsx picks the renderer based on
		// subtype; whatever the renderer is, it needs the values.
		return true
	case "httpproxy":
		// claude-code (predefined Anthropic form) and the generic
		// httpproxy renderer both surface URL/header inputs.
		return true
	case "application":
		// SSH / Git / GitHub renderers display host, user, and
		// auth-method fields. The private key itself round-trips too —
		// CLJS shows it as well, and the write-only model would mean a
		// blank "Set" badge with no way to inspect host/user either.
		switch sub {
		case "ssh", "git", "github":
			return true
		}
	}
	return false
}

// isFreeFormCustom reports whether a connection is a *pure* free-form
// custom (`type=custom` with the subtype in the free-form set or empty).
// Used only for tests and audit purposes — the strip decision goes
// through shouldRoundTripSecrets.
func isFreeFormCustom(conn *models.Connection) bool {
	if conn == nil || conn.Type != "custom" {
		return false
	}
	sub := conn.SubType.String
	if sub == "" {
		return true
	}
	return freeFormCustomSubtypes[sub]
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
		if IsSecretReference(v) || isBooleanValue(v) {
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
