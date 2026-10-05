package services

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/externaljwt"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// The claims a mapping can match.
const (
	SidecarClaimSub   = "sub"
	SidecarClaimEmail = "email"
)

// sidecarNameCapture is the placeholder a name template fills with the text
// the pattern's '*' matched.
const sidecarNameCapture = "{1}"

// sidecarCreatedByPrefix marks a sidecar a service account created, in
// created_by where an admin's email is otherwise written.
const sidecarCreatedByPrefix = "service-account:"

// SidecarDeletedMessage answers an identity whose sidecar an admin deleted.
const SidecarDeletedMessage = "this sidecar was deleted in the control plane; an admin must clear the deleted name to allow it again"

// sidecarReservedNames would shadow the static routes registered beside
// /sidecars/:nameOrID.
var sidecarReservedNames = []string{"handshake", "configuration"}

// IsReservedSidecarName reports a name no sidecar may take, whoever creates it.
func IsReservedSidecarName(name string) bool { return slices.Contains(sidecarReservedNames, name) }

// Every refusal of a service account token wraps one of these. The
// middleware answers 401 for a *SidecarIdentityRefusal and 500 for any other
// error, which is then the plane's own failure.
var (
	ErrSidecarIdentityMalformed     = errors.New("sidecar identity: not a JWT")
	ErrSidecarIdentityUnknownIssuer = errors.New("sidecar identity: unknown issuer")
	ErrSidecarIdentityInvalid       = errors.New("sidecar identity: token invalid")
	ErrSidecarIdentityNotAllowed    = errors.New("sidecar identity: subject not allowed")
	ErrSidecarIdentityAmbiguous     = errors.New("sidecar identity: matched in more than one organization")
	ErrSidecarIdentityInvalidName   = errors.New("sidecar identity: rendered name refused")
	ErrSidecarIdentityDeleted       = errors.New("sidecar identity: sidecar deleted")
	ErrSidecarIdentityBound         = errors.New("sidecar identity: sidecar bound to another service account")
	ErrSidecarIdentityHasToken      = errors.New("sidecar identity: sidecar registered with a token")
)

// SidecarIdentityUnverifiedMessage answers every refusal made before the
// token is verified. Its sender is not authenticated yet, so it learns
// nothing about the plane's mappings or key fetches.
const SidecarIdentityUnverifiedMessage = "the service account token failed verification"

// SidecarIdentityRefusal is a service account token the plane refused.
// Message is safe to send to the caller and never holds key material:
// SidecarIdentityUnverifiedMessage before the token is verified, the reason
// after.
type SidecarIdentityRefusal struct {
	Kind    error
	Message string
	// Issuer is the token's iss, verified or not. Empty when it has none.
	Issuer string
	// detail is for the plane's log only.
	detail string
}

func (r *SidecarIdentityRefusal) Error() string { return r.Message }
func (r *SidecarIdentityRefusal) Unwrap() error { return r.Kind }

// LogReason is what the plane logs: Message, with the detail the caller is
// not sent.
func (r *SidecarIdentityRefusal) LogReason() string {
	if r.detail == "" {
		return r.Message
	}
	return r.Message + ": " + r.detail
}

func refuseIdentity(kind error, format string, args ...any) *SidecarIdentityRefusal {
	return &SidecarIdentityRefusal{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// refuseUnverified refuses a token before it is verified: the reason goes to
// the plane's log only.
func refuseUnverified(kind error, format string, args ...any) *SidecarIdentityRefusal {
	return &SidecarIdentityRefusal{Kind: kind, Message: SidecarIdentityUnverifiedMessage, detail: fmt.Sprintf(format, args...)}
}

// ErrInvalidSidecarServiceAccount wraps every reason a mapping write is
// refused; the API answers it with 422.
var ErrInvalidSidecarServiceAccount = errors.New("invalid sidecar service account")

func invalidServiceAccount(field, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrInvalidSidecarServiceAccount, field, fmt.Sprintf(format, args...))
}

// isGoogleIssuer names the public issuer whose subjects are any Google
// service account of any project. Its mappings are fenced to one project.
func isGoogleIssuer(issuer string) bool {
	switch strings.TrimSuffix(issuer, "/") {
	case "https://accounts.google.com", "accounts.google.com":
		return true
	}
	return false
}

// googleServiceAccountDomain is the suffix of a Google service account email
// after "@<project>".
const googleServiceAccountDomain = ".iam.gserviceaccount.com"

// checkSubjectPattern holds the pattern rules that decide what a mapping can
// match. Writes refuse a mapping that breaks them and the matcher skips one,
// so a row written before a rule existed cannot widen what it matches.
func checkSubjectPattern(sa *models.SidecarServiceAccount) error {
	if sa.Claim != SidecarClaimSub && sa.Claim != SidecarClaimEmail {
		return invalidServiceAccount("claim", "must be %q or %q", SidecarClaimSub, SidecarClaimEmail)
	}
	pattern := sa.SubjectPattern
	if pattern == "" {
		return invalidServiceAccount("subject_pattern", "is required")
	}
	if strings.Count(pattern, "*") > 1 {
		return invalidServiceAccount("subject_pattern", "may contain at most one *")
	}
	if pattern == "*" && !sa.AllowAnySubject {
		return invalidServiceAccount("subject_pattern", "a bare * matches every subject of the issuer and needs allow_any_subject")
	}
	if isGoogleIssuer(sa.Issuer) {
		if sa.Claim != SidecarClaimEmail {
			return invalidServiceAccount("claim", "must be %q for the Google issuer", SidecarClaimEmail)
		}
		// Any Google service account of any project holds a token from this
		// issuer, so a mapping is fenced to one project: a literal domain the
		// '*' cannot reach into.
		at := strings.LastIndexByte(pattern, '@')
		domain := ""
		if at >= 0 {
			domain = pattern[at+1:]
		}
		project := strings.TrimSuffix(domain, googleServiceAccountDomain)
		if at < 0 || strings.Contains(domain, "*") || project == domain || !validGoogleProject(project) {
			return invalidServiceAccount("subject_pattern",
				"must end in a literal @<project>%s for the Google issuer, with no * after the @", googleServiceAccountDomain)
		}
	}
	return nil
}

// validGoogleProject accepts a project ID: lowercase letters, digits and
// hyphens, starting with a letter.
func validGoogleProject(project string) bool {
	if project == "" || project[0] < 'a' || project[0] > 'z' {
		return false
	}
	for _, r := range project {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// sampleCapture renders a template at write time, to check it can render a
// valid name at all.
const sampleCapture = "sample"

// ValidateSidecarServiceAccount refuses a mapping write that could not be
// served or that matches more than its admin can mean. Every error wraps
// ErrInvalidSidecarServiceAccount.
func ValidateSidecarServiceAccount(sa *models.SidecarServiceAccount) error {
	if err := apivalidation.ValidateResourceName(sa.Name); err != nil {
		return invalidServiceAccount("name", "%v", err)
	}
	if sa.Issuer == "" {
		return invalidServiceAccount("issuer", "is required")
	}
	if sa.Audience == "" {
		return invalidServiceAccount("audience", "is required")
	}
	if len(sa.JWKS) > 0 {
		if err := externaljwt.ValidateJWKS(sa.JWKS); err != nil {
			return invalidServiceAccount("jwks", "%v", err)
		}
	} else if err := checkDiscoverableIssuer(sa.Issuer); err != nil {
		return invalidServiceAccount("issuer", "%v", err)
	}
	if err := checkSubjectPattern(sa); err != nil {
		return err
	}
	if sa.NameTemplate == "" {
		return invalidServiceAccount("name_template", "is required")
	}
	capture := ""
	if strings.Contains(sa.SubjectPattern, "*") {
		capture = sampleCapture
	} else if strings.Contains(sa.NameTemplate, sidecarNameCapture) {
		return invalidServiceAccount("name_template", "%s is the text * matched, and the pattern has no *", sidecarNameCapture)
	}
	if _, err := RenderSidecarName(sa.NameTemplate, capture); err != nil {
		return invalidServiceAccount("name_template", "rendered with %s = %q: %v", sidecarNameCapture, capture, err)
	}
	return nil
}

// checkDiscoverableIssuer holds an issuer the plane fetches keys from to the
// shape OpenID Connect Discovery requires: https, a host, no query or
// fragment.
func checkDiscoverableIssuer(issuer string) error {
	u, err := url.Parse(issuer)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("must be an https URL with no query or fragment, or set jwks")
	}
	return nil
}

// RenderSidecarName fills the template and holds the result to the rules
// POST /api/sidecars applies to a name.
func RenderSidecarName(template, capture string) (string, error) {
	name := strings.ReplaceAll(template, sidecarNameCapture, capture)
	if err := apivalidation.ValidateResourceName(name); err != nil {
		return name, err
	}
	if IsReservedSidecarName(name) {
		return name, fmt.Errorf("name %q is reserved", name)
	}
	return name, nil
}

// matchSubjectPattern matches value against an exact pattern, or one with a
// single '*' that stands for one or more characters. capture is the text the
// '*' matched.
func matchSubjectPattern(pattern, value string) (capture string, ok bool) {
	star := strings.IndexByte(pattern, '*')
	if star < 0 {
		return "", value == pattern
	}
	prefix, suffix := pattern[:star], pattern[star+1:]
	if len(value) <= len(prefix)+len(suffix) ||
		!strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, suffix) {
		return "", false
	}
	return value[len(prefix) : len(value)-len(suffix)], true
}

// SidecarServiceAccountMatch is the mapping a token reached and the sidecar
// name it renders.
type SidecarServiceAccountMatch struct {
	ServiceAccount models.SidecarServiceAccount
	// Subject is the claim value matched: sub, or the verified email.
	Subject string
	Name    string
}

// MatchSidecarServiceAccount picks the mapping for a verified token among the
// mappings it verified against. A match in more than one organization is
// refused. Within one, an exact pattern beats a wildcard, then the longest
// literal text wins, then the oldest mapping. Every error is a
// *SidecarIdentityRefusal.
func MatchSidecarServiceAccount(mappings []models.SidecarServiceAccount, claims externaljwt.OIDCClaims) (*SidecarServiceAccountMatch, error) {
	var (
		best        *SidecarServiceAccountMatch
		capture     string
		orgs        []string
		unverified  bool
		lastSubject string
	)
	for i := range mappings {
		sa := &mappings[i]
		if checkSubjectPattern(sa) != nil {
			continue
		}
		value := claims.Subject
		if sa.Claim == SidecarClaimEmail {
			value = claims.Email
			if value != "" && !claims.EmailVerified {
				unverified = true
				continue
			}
		}
		if value == "" {
			continue
		}
		lastSubject = value
		c, ok := matchSubjectPattern(sa.SubjectPattern, value)
		if !ok {
			continue
		}
		if !slices.Contains(orgs, sa.OrgID) {
			orgs = append(orgs, sa.OrgID)
		}
		if best == nil || betterServiceAccount(sa, &best.ServiceAccount) {
			best = &SidecarServiceAccountMatch{ServiceAccount: *sa, Subject: value}
			capture = c
		}
	}

	switch {
	case len(orgs) > 1:
		r := refuseIdentity(ErrSidecarIdentityAmbiguous,
			"the token matches sidecar service accounts in more than one organization; an admin must narrow the patterns")
		r.detail = fmt.Sprintf("orgs=%s subject=%q email=%q", strings.Join(orgs, ","), claims.Subject, claims.Email)
		return nil, r
	case best == nil && unverified:
		return nil, refuseIdentity(ErrSidecarIdentityNotAllowed,
			"the token's email is not verified (email_verified is not true)")
	case best == nil && lastSubject == "":
		return nil, refuseIdentity(ErrSidecarIdentityNotAllowed,
			"the token carries no claim a sidecar service account of this issuer matches")
	case best == nil:
		return nil, refuseIdentity(ErrSidecarIdentityNotAllowed,
			"no sidecar service account of this issuer allows subject %q; check the mapping's audience and pattern", lastSubject)
	}

	name, err := RenderSidecarName(best.ServiceAccount.NameTemplate, capture)
	if err != nil {
		return nil, refuseIdentity(ErrSidecarIdentityInvalidName,
			"sidecar service account %q renders the sidecar name %q, which is refused: %v",
			best.ServiceAccount.Name, name, err)
	}
	best.Name = name
	return best, nil
}

// betterServiceAccount orders two mappings that both matched in one
// organization.
func betterServiceAccount(a, b *models.SidecarServiceAccount) bool {
	aExact, bExact := !strings.Contains(a.SubjectPattern, "*"), !strings.Contains(b.SubjectPattern, "*")
	if aExact != bExact {
		return aExact
	}
	aLit := utf8.RuneCountInString(a.SubjectPattern) - strings.Count(a.SubjectPattern, "*")
	bLit := utf8.RuneCountInString(b.SubjectPattern) - strings.Count(b.SubjectPattern, "*")
	if aLit != bLit {
		return aLit > bLit
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID < b.ID
}

// AuthenticateSidecarIdentity resolves the sidecar a service account token
// reaches: the mappings for the token's issuer, across every
// organization, then verification, matching, and the sidecar with the
// rendered name, bound to the first identity that reaches it (see
// models.GetOrCreateSidecarForIdentity).
//
// An issuer no mapping names is refused before anything is fetched: the
// issuer of an unverified token is whatever its sender wrote. Every refusal
// carries that issuer for the plane's log.
func AuthenticateSidecarIdentity(ctx context.Context, db *gorm.DB, verifier *externaljwt.OIDCVerifier, raw string) (*models.Sidecar, error) {
	issuer, err := externaljwt.UnverifiedIssuer(raw)
	if err != nil {
		return nil, refuseUnverified(ErrSidecarIdentityMalformed, "not a JWT with an issuer: %v", err)
	}
	sc, err := authenticateSidecarIdentity(ctx, db, verifier, raw, issuer)
	var r *SidecarIdentityRefusal
	if errors.As(err, &r) {
		r.Issuer = issuer
	}
	return sc, err
}

func authenticateSidecarIdentity(ctx context.Context, db *gorm.DB, verifier *externaljwt.OIDCVerifier, raw, issuer string) (*models.Sidecar, error) {
	mappings, err := models.ListSidecarServiceAccountsByIssuer(db, issuer)
	if err != nil {
		return nil, fmt.Errorf("failed listing sidecar service accounts: %w", err)
	}
	if len(mappings) == 0 {
		return nil, refuseUnverified(ErrSidecarIdentityUnknownIssuer,
			"no sidecar service account is configured for this issuer")
	}

	verified, claims, err := verifySidecarIdentity(ctx, verifier, raw, issuer, mappings)
	if err != nil {
		return nil, err
	}
	match, err := MatchSidecarServiceAccount(verified, *claims)
	if err != nil {
		return nil, err
	}

	sa := match.ServiceAccount
	sc, err := models.GetOrCreateSidecarForIdentity(db, sa.OrgID, match.Name, issuer, match.Subject,
		sidecarCreatedByPrefix+match.Subject, sa.AdoptExistingSidecars)
	switch {
	case errors.Is(err, models.ErrSidecarNameDeleted):
		return nil, refuseIdentity(ErrSidecarIdentityDeleted, "%s", SidecarDeletedMessage)
	case errors.Is(err, models.ErrSidecarBoundToAnotherIdentity):
		r := refuseIdentity(ErrSidecarIdentityBound,
			"sidecar %q is bound to another service account; an admin must clear its binding "+
				"(DELETE /api/sidecars/%s/identity) before this one can reach it", match.Name, match.Name)
		r.detail = fmt.Sprintf("subject=%q", match.Subject)
		return nil, r
	case errors.Is(err, models.ErrSidecarHasToken):
		r := refuseIdentity(ErrSidecarIdentityHasToken,
			"sidecar %q was registered with a token; set adopt_existing_sidecars on the sidecar service account "+
				"entry to let service accounts reach it", match.Name)
		r.detail = fmt.Sprintf("subject=%q mapping=%q", match.Subject, sa.Name)
		return nil, r
	case err != nil:
		return nil, fmt.Errorf("failed resolving sidecar %q for a service account: %w", match.Name, err)
	}
	return sc, nil
}

// verifySidecarIdentity verifies raw against every mapping and returns the
// ones it verified against. Mappings differ in audience and key source, so
// one token can verify for some and not others; each distinct pair is checked
// once. The claims are the token's, the same whichever mapping verified it.
func verifySidecarIdentity(ctx context.Context, verifier *externaljwt.OIDCVerifier, raw, issuer string,
	mappings []models.SidecarServiceAccount) ([]models.SidecarServiceAccount, *externaljwt.OIDCClaims, error) {
	type outcome struct {
		claims *externaljwt.OIDCClaims
		err    error
	}
	seen := map[string]outcome{}
	var (
		verified []models.SidecarServiceAccount
		claims   *externaljwt.OIDCClaims
		firstErr error
	)
	for _, sa := range mappings {
		jwks := bytes.TrimSpace(sa.JWKS)
		sum := sha256.Sum256(jwks)
		key := sa.Audience + "\x00" + string(sum[:])
		out, ok := seen[key]
		if !ok {
			out.claims, out.err = verifier.Verify(ctx, raw, issuer, sa.Audience, jwks)
			seen[key] = out
		}
		if out.err != nil {
			if firstErr == nil {
				firstErr = out.err
			}
			continue
		}
		claims = out.claims
		verified = append(verified, sa)
	}
	if len(verified) == 0 {
		return nil, nil, refuseUnverified(ErrSidecarIdentityInvalid, "%v", firstErr)
	}
	return verified, claims, nil
}
