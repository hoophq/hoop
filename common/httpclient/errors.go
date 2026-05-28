package httpclient

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"syscall"
)

// HumanizeNetError converts a raw error returned by (*http.Client).Do into
// a single sentence with actionable next steps for the user. apiURL is
// the human-facing endpoint the CLI is configured against (the value of
// the user's `api_url`, NOT the full request URL — internal paths like
// /api/serverinfo are not actionable for users).
//
// If err can't be classified the original error is wrapped with %w so
// `errors.Is/As` remains useful for callers and the message is still
// shown in a clean form.
func HumanizeNetError(apiURL string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf(
			"cannot reach the hoop gateway at %s — the host is up but nothing is listening on that port.\n"+
				"  - is the gateway process running?\n"+
				"  - does `api_url` in `hoop config view` point at the right host:port?\n"+
				"  - try `127.0.0.1` instead of `localhost` if the gateway only binds IPv4",
			apiURL,
		)
	case isDNSError(err):
		return fmt.Errorf(
			"cannot resolve the gateway host in %s — DNS lookup failed.\n"+
				"  - check your network / DNS settings\n"+
				"  - re-point with: hoop config create --api-url <url>",
			apiURL,
		)
	case isTimeout(err):
		return fmt.Errorf(
			"timed out reaching the hoop gateway at %s — the gateway is unreachable or too slow to respond.\n"+
				"  - check your network connectivity\n"+
				"  - verify the gateway is healthy",
			apiURL,
		)
	case isTLSError(err):
		return fmt.Errorf(
			"TLS handshake failed when reaching the hoop gateway at %s.\n"+
				"  - if you trust this gateway, pass --skip-tls-verify or set HOOP_TLS_SKIP_VERIFY=true\n"+
				"  - or provide a CA bundle with HOOP_TLSCA",
			apiURL,
		)
	default:
		return fmt.Errorf("cannot reach the hoop gateway at %s: %w", apiURL, err)
	}
}

func isDNSError(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}

func isTimeout(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

func isTLSError(err error) bool {
	var recordErr tls.RecordHeaderError
	if errors.As(err, &recordErr) {
		return true
	}
	var certInvalidErr x509.CertificateInvalidError
	if errors.As(err, &certInvalidErr) {
		return true
	}
	var unknownAuthErr x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthErr) {
		return true
	}
	var hostnameErr x509.HostnameError
	return errors.As(err, &hostnameErr)
}
