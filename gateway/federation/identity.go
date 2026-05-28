package federation

import (
	"fmt"
	"strings"
)

// IdentityContext is the input to the mapping engine. Fields mirror the
// JSONPath sources documented in PRD §5.2.3. New attributes are added here
// (and to the template substitution map below) when customers ask for them.
type IdentityContext struct {
	UserEmail string
	UserID    string
}

// supportedSourceAttributes is the closed set of JSONPath-ish expressions the
// v1 mapping engine accepts. Restricting the surface keeps mapping
// deterministic and avoids shipping a full JSONPath dependency for a feature
// that only needs to address two fields today.
var supportedSourceAttributes = map[string]func(IdentityContext) string{
	"$.user.email": func(c IdentityContext) string { return c.UserEmail },
	"$.user.id":    func(c IdentityContext) string { return c.UserID },
}

// ResolveIdentity computes the target principal from the configured source
// attribute and target template. It returns the principal string (e.g.
// "user@acme.com") and an error if the source attribute is unsupported, the
// source value is empty, or the template references an unknown placeholder.
//
// Resolution rules:
//
//  1. The source attribute must be one of supportedSourceAttributes.
//  2. The source value extracted from IdentityContext must be non-empty.
//  3. The target template can contain {user.email}, {user.email_local}
//     (everything before the last "@" in UserEmail) and {user.id}
//     placeholders, plus an unbraced literal. Unknown placeholders error
//     loudly rather than silently rendering "{user.foo}" into the principal.
//  4. Empty source/result values fail the resolution. Callers should apply
//     the configured fallback policy.
func ResolveIdentity(srcAttr, targetTemplate string, ctx IdentityContext) (string, error) {
	if srcAttr == "" {
		srcAttr = "$.user.email"
	}
	getter, ok := supportedSourceAttributes[srcAttr]
	if !ok {
		return "", fmt.Errorf("unsupported identity source attribute %q (supported: $.user.email, $.user.id)", srcAttr)
	}
	srcValue := getter(ctx)
	if srcValue == "" {
		return "", fmt.Errorf("identity source attribute %q resolved to empty value", srcAttr)
	}

	if targetTemplate == "" {
		targetTemplate = "{user.email}"
	}
	rendered, err := renderIdentityTemplate(targetTemplate, ctx)
	if err != nil {
		return "", err
	}
	if rendered == "" {
		return "", fmt.Errorf("identity target template %q rendered to empty principal", targetTemplate)
	}
	return rendered, nil
}

// renderIdentityTemplate substitutes a small fixed set of placeholders inside
// a template string. The format is intentionally minimal: full templating
// (conditions, loops) is out of scope for v1.
//
// {user.email_local} is a convenience helper for the common case of building a
// GCP service-account email from a human email: it returns everything before
// the last "@" in UserEmail. For input "alice@acme.com" it renders "alice",
// which paired with a literal "@<project>.iam.gserviceaccount.com" suffix
// produces a valid SA email without forcing the operator to also model a
// separate "handle" attribute on the Hoop user.
//
// If UserEmail does not contain "@" the helper falls back to the full
// UserEmail; downstream resolvers (e.g. gcpiam) validate whether the rendered
// string is a legal principal for their domain.
func renderIdentityTemplate(tpl string, ctx IdentityContext) (string, error) {
	substitutions := map[string]string{
		"{user.email}":       ctx.UserEmail,
		"{user.email_local}": emailLocalPart(ctx.UserEmail),
		"{user.id}":          ctx.UserID,
	}

	out := tpl
	for placeholder, value := range substitutions {
		out = strings.ReplaceAll(out, placeholder, value)
	}

	// If any unresolved {...} braces remain, the template referenced a
	// placeholder we don't support. Fail loudly so the admin sees the typo
	// rather than producing "user@{user.foo}.com" silently.
	if openIdx := strings.Index(out, "{"); openIdx >= 0 {
		closeIdx := strings.Index(out[openIdx:], "}")
		if closeIdx >= 0 {
			unknown := out[openIdx : openIdx+closeIdx+1]
			return "", fmt.Errorf("identity target template contains unknown placeholder %s (supported: {user.email}, {user.email_local}, {user.id})", unknown)
		}
	}
	return out, nil
}

// emailLocalPart returns the substring before the last "@" in email. Splitting
// on the LAST "@" (rather than the first) is deliberate: GCP SA emails
// themselves embed an "@" in the suffix, so any template renderer chaining
// {user.email_local} with a literal suffix should walk back from the right.
// Inputs without an "@" pass through unchanged so callers that already supply
// a local part keep working.
func emailLocalPart(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return email
	}
	return email[:at]
}
