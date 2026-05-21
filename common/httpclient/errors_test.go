package httpclient

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"
)

const testAPIURL = "http://localhost:8009"

func TestHumanizeNetErrorConnectionRefused(t *testing.T) {
	// Mirrors the wrapping produced by net/http when dial fails:
	// url.Error -> net.OpError -> os.SyscallError -> syscall.ECONNREFUSED.
	innerOpErr := &net.OpError{
		Op:  "dial",
		Net: "tcp",
		Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED},
	}
	raw := &url.Error{Op: "Get", URL: testAPIURL + "/api/serverinfo", Err: innerOpErr}

	got := HumanizeNetError(testAPIURL, raw)
	if got == nil {
		t.Fatal("expected error")
	}
	msg := got.Error()
	if !strings.Contains(msg, "cannot reach the hoop gateway at "+testAPIURL) {
		t.Errorf("message should mention apiURL: %q", msg)
	}
	if strings.Contains(msg, "/api/serverinfo") {
		t.Errorf("message should not leak the internal URL path: %q", msg)
	}
	if !strings.Contains(msg, "is the gateway process running?") {
		t.Errorf("message should suggest checking the gateway process: %q", msg)
	}
}

func TestHumanizeNetErrorDNS(t *testing.T) {
	raw := &url.Error{Op: "Get", URL: testAPIURL, Err: &net.DNSError{
		Err:  "no such host",
		Name: "gateway.invalid",
	}}
	msg := HumanizeNetError(testAPIURL, raw).Error()
	if !strings.Contains(msg, "DNS lookup failed") {
		t.Errorf("expected DNS branch, got: %q", msg)
	}
	if !strings.Contains(msg, "hoop config create --api-url") {
		t.Errorf("expected recovery hint: %q", msg)
	}
}

// fakeTimeout implements net.Error so url.Error.Timeout() returns true.
type fakeTimeout struct{}

func (fakeTimeout) Error() string   { return "i/o timeout" }
func (fakeTimeout) Timeout() bool   { return true }
func (fakeTimeout) Temporary() bool { return true }

func TestHumanizeNetErrorTimeout(t *testing.T) {
	raw := &url.Error{Op: "Get", URL: testAPIURL, Err: fakeTimeout{}}
	msg := HumanizeNetError(testAPIURL, raw).Error()
	if !strings.Contains(msg, "timed out reaching the hoop gateway") {
		t.Errorf("expected timeout branch, got: %q", msg)
	}
}

func TestHumanizeNetErrorTLS(t *testing.T) {
	raw := &url.Error{Op: "Get", URL: "https://example.com", Err: x509.UnknownAuthorityError{}}
	msg := HumanizeNetError("https://example.com", raw).Error()
	if !strings.Contains(msg, "TLS handshake failed") {
		t.Errorf("expected TLS branch, got: %q", msg)
	}
	if !strings.Contains(msg, "HOOP_TLS_SKIP_VERIFY") {
		t.Errorf("expected hint about HOOP_TLS_SKIP_VERIFY: %q", msg)
	}
}

func TestHumanizeNetErrorGenericFallback(t *testing.T) {
	raw := errors.New("something weird happened")
	out := HumanizeNetError(testAPIURL, raw)
	msg := out.Error()
	if !strings.Contains(msg, testAPIURL) {
		t.Errorf("fallback should still mention apiURL: %q", msg)
	}
	if !strings.Contains(msg, "something weird happened") {
		t.Errorf("fallback should preserve the original error: %q", msg)
	}
	// Must remain unwrappable so errors.Is/As keeps working upstream.
	if !errors.Is(out, raw) {
		t.Errorf("fallback should wrap original via %%w")
	}
}

func TestHumanizeNetErrorNil(t *testing.T) {
	if HumanizeNetError(testAPIURL, nil) != nil {
		t.Errorf("nil input must return nil")
	}
}

// TestRealConnectionRefused proves the helper works against an *actually*
// observed network error, not just synthesised wrappers. We start a TCP
// listener, capture its address, close it, then attempt a connection so
// the kernel returns ECONNREFUSED for real.
func TestRealConnectionRefused(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := l.Addr().String()
	l.Close()

	_, err = net.Dial("tcp", addr)
	if err == nil {
		t.Fatalf("expected dial error against closed port")
	}
	apiURL := fmt.Sprintf("http://%s", addr)
	out := HumanizeNetError(apiURL, &url.Error{Op: "Get", URL: apiURL + "/api/serverinfo", Err: err})
	if !strings.Contains(out.Error(), "is the gateway process running?") {
		t.Errorf("real ECONNREFUSED should hit the refused branch: %q", out)
	}
}
