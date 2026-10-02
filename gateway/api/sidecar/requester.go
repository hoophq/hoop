package apisidecar

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hoophq/hoop/gateway/api/openapi"
	slackservice "github.com/hoophq/hoop/gateway/slack"
)

// requester is the caller who filed a sidecar review, cleaned for storage and display.
// It is display data only. Nothing that decides who may approve reads it.
type requester struct{ subject, email, peerAddr, method string }

const (
	// maxRequesterBytes bounds each stored value. The sidecar applies the same bound.
	maxRequesterBytes = 255

	// The session labels that hold the filer. webapp_v2 reads the same keys (helpers.js).
	requesterLabelSubject  = "sidecar.requester.subject"
	requesterLabelEmail    = "sidecar.requester.email"
	requesterLabelPeerAddr = "sidecar.requester.peer_addr"
	requesterLabelMethod   = "sidecar.requester.method"

	requesterMethodPeerAddress  = "peer_address"
	requesterMethodUnspecified  = "unspecified"
	requesterMethodUnrecognized = "unrecognized"
	maxRequesterMethodBytes     = 64
)

var requesterMethodPattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// newRequester cleans what the sidecar sent. It is nil when the body names nobody and no address.
// It never refuses a value, because a refusal makes the sidecar deny the statement.
func newRequester(r *openapi.SidecarReviewRequester) *requester {
	if r == nil {
		return nil
	}
	c := &requester{
		subject:  cleanRequesterValue(r.Subject),
		email:    cleanRequesterValue(r.Email),
		peerAddr: cleanRequesterValue(r.PeerAddr),
	}
	if c.subject == "" && c.email == "" && c.peerAddr == "" {
		return nil
	}
	c.method = normalizeMethod(r.Method, c.subject != "" || c.email != "")
	return c
}

// cleanRequesterValue replaces each rune that is not printable with U+FFFD, trims it,
// and cuts it to maxRequesterBytes on a rune boundary.
func cleanRequesterValue(s string) string {
	s = strings.Map(func(r rune) rune {
		if strconv.IsPrint(r) {
			return r
		}
		return utf8.RuneError
	}, s)
	s = strings.TrimSpace(s)
	if len(s) <= maxRequesterBytes {
		return s
	}
	cut := maxRequesterBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// normalizeMethod lowercases the method. A caller with no name is peer_address,
// a named caller with no method or with peer_address is unspecified.
func normalizeMethod(m string, named bool) string {
	if !named {
		return requesterMethodPeerAddress
	}
	if len(m) > maxRequesterMethodBytes {
		return requesterMethodUnrecognized
	}
	m = strings.ToLower(m)
	switch {
	case m == "" || m == requesterMethodPeerAddress:
		return requesterMethodUnspecified
	case !requesterMethodPattern.MatchString(m):
		return requesterMethodUnrecognized
	}
	return m
}

// labels are the session labels that store the filer. Nil for no filer.
func (r *requester) labels() map[string]string {
	if r == nil {
		return nil
	}
	labels := map[string]string{}
	for key, value := range map[string]string{
		requesterLabelSubject:  r.subject,
		requesterLabelEmail:    r.email,
		requesterLabelPeerAddr: r.peerAddr,
		requesterLabelMethod:   r.method,
	} {
		if value != "" {
			labels[key] = value
		}
	}
	return labels
}

// principal is the one value that names the caller in a log line.
func (r *requester) principal() string {
	switch {
	case r == nil:
		return ""
	case r.subject != "":
		return r.subject
	case r.email != "":
		return r.email
	}
	return r.peerAddr
}

// slackFiler is the filer section of the Slack message. Nil for no filer.
func (r *requester) slackFiler() *slackservice.ReviewFiler {
	if r == nil {
		return nil
	}
	f := &slackservice.ReviewFiler{
		Source:   requesterMethodLabel(r.method),
		Subject:  r.subject,
		PeerAddr: r.peerAddr,
	}
	if r.email != r.subject {
		f.Email = r.email
	}
	return f
}

// requesterMethodLabel says how the sidecar established the caller.
// Keep in sync with REQUESTER_METHOD in webapp_v2/src/pages/Reviews/helpers.js.
func requesterMethodLabel(m string) string {
	switch m {
	case "google_identity":
		return "Google token holder, checked with Google by the sidecar (audience not checked)"
	case "ssh_certificate":
		return "SSH certificate signed by the listener's trusted CA, checked by the sidecar"
	case "database_user":
		return "Login name the client claimed before authenticating, not checked"
	case "identity_header":
		return "Header value; any client that can reach the listener can set it unless a proxy strips it"
	case "tls_client_certificate":
		return "TLS certificate name, not verified"
	case requesterMethodUnspecified:
		return "Identity source not reported"
	case requesterMethodPeerAddress:
		return "Network address only, no identity"
	}
	return "Source reported as " + m
}
