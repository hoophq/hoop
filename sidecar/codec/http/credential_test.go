package http_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
)

const credentialKey = "hoop.credential"

// The credential leaves the statement: policy input and the audit trail see
// a handle, never the token, and the gate trades the handle for the value
// exactly once.
func TestCredentialIsLiftedOutOfTheStatement(t *testing.T) {
	c := codechttp.New(codechttp.Options{Headers: []string{"accept"}, CredentialHeader: "Authorization"})
	s := decodeRequest(t, c, "GET /x HTTP/1.1\r\nHost: h\r\nAccept: application/json\r\nAuthorization: Bearer ya29.secret\r\n\r\n")

	if got := s.HTTP.Headers; len(got) != 1 || got["accept"] != "application/json" {
		t.Errorf("captured headers = %v, want only the operator's accept", got)
	}
	handle, ok := s.Metadata[credentialKey]
	if !ok {
		t.Fatal("the request carries no credential handle")
	}
	for k, v := range s.Metadata {
		if strings.Contains(v, "ya29") {
			t.Errorf("metadata %q carries the credential: %q", k, v)
		}
	}
	if handle == "" {
		t.Error("the handle is empty")
	}

	got, ok := c.TakeCredential(&s)
	if !ok || got != "Bearer ya29.secret" {
		t.Fatalf("TakeCredential = %q, %v", got, ok)
	}
	if _, left := s.Metadata[credentialKey]; left {
		t.Error("the handle stayed in the statement after it was taken")
	}
	if got, ok := c.TakeCredential(&s); ok || got != "" {
		t.Errorf("a second TakeCredential = %q, %v; the value must be handed out once", got, ok)
	}

	// A copy that kept the handle cannot redeem it either.
	s2 := decodeRequest(t, c, "GET /y HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer two\r\n\r\n")
	copied := s2
	copied.Metadata = map[string]string{credentialKey: s2.Metadata[credentialKey]}
	if _, ok := c.TakeCredential(&s2); !ok {
		t.Fatal("the second request's credential was not held")
	}
	if got, ok := c.TakeCredential(&copied); ok {
		t.Errorf("a taken handle was redeemed again for %q", got)
	}
}

// Lifting is what put the header on libhoop's allowlist, so when that is the
// only reason it is there nothing else sees it: an emptied map is nil, as
// libhoop reports no captured headers. When the operator allowlisted it too,
// it stays where they asked for it.
func TestCredentialStaysCapturedOnlyWhenTheOperatorAskedForIt(t *testing.T) {
	const req = "GET /x HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer tok\r\n\r\n"

	lifted := codechttp.New(codechttp.Options{CredentialHeader: "authorization"})
	s := decodeRequest(t, lifted, req)
	if s.HTTP.Headers != nil {
		t.Errorf("captured headers = %v, want nil", s.HTTP.Headers)
	}
	if got, ok := lifted.TakeCredential(&s); !ok || got != "Bearer tok" {
		t.Errorf("TakeCredential = %q, %v", got, ok)
	}

	kept := codechttp.New(codechttp.Options{Headers: []string{"Authorization"}, CredentialHeader: "authorization"})
	s = decodeRequest(t, kept, req)
	if s.HTTP.Headers["authorization"] != "Bearer tok" {
		t.Errorf("captured headers = %v, want the allowlisted authorization", s.HTTP.Headers)
	}
	if got, ok := kept.TakeCredential(&s); !ok || got != "Bearer tok" {
		t.Errorf("TakeCredential = %q, %v", got, ok)
	}
}

// Every request gets a handle, so "a request without a credential" reads
// differently from "not a request". A header sent twice arrives joined, and
// goes to the resolver unchanged for it to refuse. Responses get no handle.
func TestCredentialHandlesFollowRequests(t *testing.T) {
	c := codechttp.New(codechttp.Options{CredentialHeader: "authorization"})

	s := decodeRequest(t, c, "GET /x HTTP/1.1\r\nHost: h\r\n\r\n")
	if got, ok := c.TakeCredential(&s); !ok || got != "" {
		t.Errorf("a request with no credential: TakeCredential = %q, %v; want \"\", true", got, ok)
	}

	s = decodeRequest(t, c, "GET /x HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer a\r\nAuthorization: Bearer b\r\n\r\n")
	if got, ok := c.TakeCredential(&s); !ok || got != "Bearer a, Bearer b" {
		t.Errorf("a repeated header: TakeCredential = %q, %v; want the joined value", got, ok)
	}

	stmts, _, err := c.Decode(inspect.FromServer, []byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\nAuthorization: echoed\r\n\r\n"))
	if err != nil || len(stmts) != 1 {
		t.Fatalf("Decode response: %d statements, %v", len(stmts), err)
	}
	if stmts[0].HTTP.Headers != nil {
		t.Errorf("a response header captured only for lifting reached the statement: %v", stmts[0].HTTP.Headers)
	}
	if got, ok := c.TakeCredential(&stmts[0]); ok {
		t.Errorf("a response yielded a credential %q", got)
	}

	plain := codechttp.New(codechttp.Options{})
	s = decodeRequest(t, plain, "GET /x HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer tok\r\n\r\n")
	if got, ok := plain.TakeCredential(&s); ok {
		t.Errorf("a codec with no credential header lifted %q", got)
	}
}

// Credentials lifted and never taken cannot pile up for the life of the
// connection. Past the bound the codec refuses the stream in the one way
// the gate cannot forward around, and still keeps the value out of the
// statement it returns.
func TestUntakenCredentialsFailTheStreamClosed(t *testing.T) {
	c := codechttp.New(codechttp.Options{CredentialHeader: "authorization"})
	req := func(i int) []byte {
		return fmt.Appendf(nil, "GET /%d HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer %d\r\n\r\n", i, i)
	}
	var first inspect.Statement
	for i := range 1024 {
		stmts, _, err := c.Decode(inspect.FromClient, req(i))
		if err != nil {
			t.Fatalf("request %d refused below the bound: %v", i, err)
		}
		if i == 0 {
			first = stmts[0]
		}
	}

	stmts, _, err := c.Decode(inspect.FromClient, req(1024))
	if !errors.Is(err, inspect.ErrStreamUnsafe) {
		t.Fatalf("past the bound: err = %v, want inspect.ErrStreamUnsafe", err)
	}
	if len(stmts) == 1 && stmts[0].HTTP.Headers != nil {
		t.Errorf("the refused request still carries the credential: %v", stmts[0].HTTP.Headers)
	}

	// Taking one frees its slot.
	if _, ok := c.TakeCredential(&first); !ok {
		t.Fatal("the first credential was lost")
	}
	if _, _, err := c.Decode(inspect.FromClient, req(1025)); err != nil {
		t.Errorf("a freed slot was not reused: %v", err)
	}
}
