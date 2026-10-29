package daemon

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// An http listener may write listen and upstream as URLs, the way Caddy and
// nginx take them: `upstream: https://api.example.com` instead of
// `upstream: api.example.com:443` plus an upstream_tls block. The URL form
// is more than spelling: it makes the lane a reverse proxy in front of that
// origin, so every request names the origin in Host (codec/http origin.go).
// That is what lets a browser reach an origin through the sidecar, and it is
// opt-in through the form so a host:port lane forwards Host as it always
// has: an operator who fronts a virtual-hosted backend with host:port today
// relies on the client's Host arriving unchanged.
//
// Both values stay as written in the config. The address the lane binds or
// dials is derived from them here, so the document an admin reads back is
// the one they wrote, and an older sidecar is refused a URL it would try to
// dial verbatim (CapabilityAddressURL).

// addressURL is a listen or upstream value written as a URL.
type addressURL struct {
	scheme string // "http" or "https"
	host   string // the host name or IP, an IPv6 address without brackets
	port   string // as written, or the scheme's default
}

// parseAddressURL reads v as a URL address. isURL is false, with no error,
// when v is not written as one: a host:port, or a socket path.
//
// Only an origin is accepted, scheme://host[:port] with at most a trailing
// slash. A path would suggest a prefix the relay does not route by, and
// credentials in a URL would sit in plain text in every config view.
func parseAddressURL(v string) (a addressURL, isURL bool, err error) {
	if !strings.Contains(v, "://") {
		return addressURL{}, false, nil
	}
	u, err := url.Parse(v)
	if err != nil {
		return addressURL{}, true, fmt.Errorf("%q is not a valid URL", v)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return addressURL{}, true, fmt.Errorf("%q: the scheme must be http or https", v)
	}
	if u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return addressURL{}, true, fmt.Errorf("%q: write the origin only, %s://host[:port]; a path, query, fragment or user is not supported", v, u.Scheme)
	}
	if u.Hostname() == "" {
		return addressURL{}, true, fmt.Errorf("%q names no host", v)
	}
	// Stored canonical, so "00080" is port 80: authority compares it with
	// the scheme's default, and the Host it writes must be the one a
	// browser would send.
	port := defaultPort(u.Scheme)
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n > 65535 {
			return addressURL{}, true, fmt.Errorf("%q: port %s is out of range", v, p)
		}
		port = strconv.Itoa(n)
	}
	return addressURL{scheme: u.Scheme, host: u.Hostname(), port: port}, true, nil
}

func defaultPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// hostPort is the address to bind or dial.
func (a addressURL) hostPort() string { return net.JoinHostPort(a.host, a.port) }

// authority is the address as a Host field writes it: the port only when it
// is not the scheme's default, which is what a browser sends for the same
// URL.
func (a addressURL) authority() string {
	if a.port != defaultPort(a.scheme) {
		return a.hostPort()
	}
	if strings.Contains(a.host, ":") {
		return "[" + a.host + "]"
	}
	return a.host
}

// listenAddress is the address the lane binds: Listen, or the host:port its
// URL names. A URL that does not parse is returned as written; validate has
// refused it already.
func (l ListenerConfig) listenAddress() string {
	if a, isURL, err := parseAddressURL(l.Listen); isURL && err == nil {
		return a.hostPort()
	}
	return l.Listen
}

// upstreamAddress is the address the lane dials: Upstream, or the host:port
// its URL names.
func (l ListenerConfig) upstreamAddress() string {
	if a, isURL, err := parseAddressURL(l.Upstream); isURL && err == nil {
		return a.hostPort()
	}
	return l.Upstream
}

// upstreamTLS is the TLS the lane originates: upstream_tls as written, or,
// for an https upstream URL without it, an empty block. An empty block
// verifies the origin against the trust store with the URL's host as SNI,
// which is what the scheme promises.
func (l ListenerConfig) upstreamTLS() *TLSConfig {
	if l.UpstreamTLS != nil {
		return l.UpstreamTLS
	}
	if a, isURL, err := parseAddressURL(l.Upstream); isURL && err == nil && a.scheme == "https" {
		return &TLSConfig{}
	}
	return nil
}

// origin is the http codec's reverse-proxy target: set when upstream is a
// URL, nil when it is host:port.
func (l ListenerConfig) origin() *codechttp.Origin {
	a, isURL, err := parseAddressURL(l.Upstream)
	if !isURL || err != nil {
		return nil
	}
	proto := "http"
	if l.DownstreamTLS != nil {
		proto = "https"
	}
	return &codechttp.Origin{Host: a.authority(), Proto: proto}
}

// usesAddressURL reports whether listen or upstream is written as a URL.
func (l ListenerConfig) usesAddressURL() bool {
	_, listenURL, _ := parseAddressURL(l.Listen)
	_, upstreamURL, _ := parseAddressURL(l.Upstream)
	return listenURL || upstreamURL
}

// addressProblems checks a listener's URL-form listen and upstream. A
// host:port value is not checked: it is dialed as written, as it always was.
func (l ListenerConfig) addressProblems(name string) []string {
	if isMCPGateway(l) {
		return nil // an mcp upstream is an endpoint URL with a path; validateMCPGatewayLane checks it
	}
	var problems []string
	listen, listenURL, listenErr := parseAddressURL(l.Listen)
	if listenErr != nil {
		problems = append(problems, fmt.Sprintf("%s: listen %v", name, listenErr))
	}
	upstream, upstreamURL, upstreamErr := parseAddressURL(l.Upstream)
	if upstreamErr != nil {
		problems = append(problems, fmt.Sprintf("%s: upstream %v", name, upstreamErr))
	}
	if !listenURL && !upstreamURL {
		return problems
	}
	if inspect.Protocol(l.Protocol) != inspect.HTTP {
		return append(problems, fmt.Sprintf(
			"%s: listen and upstream take a URL on an http listener only; write host:port on a %s listener",
			name, l.Protocol))
	}
	// A URL that did not parse is reported above; the checks below read
	// its parts, which it has none of.
	if listenURL && listenErr == nil {
		switch {
		case l.Network == "unix":
			problems = append(problems, fmt.Sprintf(
				"%s: a unix listener binds a socket path, not a URL", name))
		case listen.scheme == "https" && l.DownstreamTLS == nil:
			problems = append(problems, fmt.Sprintf(
				"%s: listen %s serves TLS, which needs downstream_tls with cert_file and key_file; "+
					"or write http://", name, l.Listen))
		case listen.scheme == "http" && l.DownstreamTLS != nil:
			problems = append(problems, fmt.Sprintf(
				"%s: listen %s is plaintext, and downstream_tls serves TLS; write https://, "+
					"or drop downstream_tls", name, l.Listen))
		}
	}
	if upstreamURL && upstreamErr == nil && upstream.scheme == "http" && l.UpstreamTLS != nil {
		problems = append(problems, fmt.Sprintf(
			"%s: upstream %s is plaintext, and upstream_tls encrypts that hop; write https://, "+
				"or drop upstream_tls", name, l.Upstream))
	}
	return problems
}
