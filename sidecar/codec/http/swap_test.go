package http_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

const upstreamToken = "ya29.upstream-token"

// bearer is the caller's Authorization value as the check and the resolver
// see it.
const bearer = "Bearer " + secret

// swapSecret is the resolver a daemon would compose: the caller's bearer
// resolves to the upstream's bearer token for alice, anything else is
// refused.
func swapSecret(_ context.Context, credential string) (codechttp.SwapResult, error) {
	if credential != bearer {
		return codechttp.SwapResult{}, fmt.Errorf("%w: the caller token is not accepted", codectypes.ErrLoginRefused)
	}
	return codechttp.SwapResult{
		Password: upstreamToken,
		Bearer:   true,
		Identity: session.Identity{Subject: "sub-1", Email: "alice@example.com", Groups: []string{"dev", "ops"}},
	}, nil
}

// authSwap is a lane that authenticates its caller by Authorization and
// swaps it for the upstream credential.
var authSwap = codechttp.Options{CredentialHeader: "Authorization", AuthenticatesCaller: true, Swap: swapSecret}

// swapCodec builds a swapping codec; on an authenticating lane the check
// accepts bearer only, as the gate would install its own.
func swapCodec(t *testing.T, o codechttp.Options) *codechttp.Codec {
	t.Helper()
	c := codechttp.New(o)
	if o.AuthenticatesCaller {
		err := c.AuthenticateCallers(func(credential string) error {
			if credential != bearer {
				return errNotListed
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// swapFilter runs in through a fresh swapping codec, split at every point
// and byte by byte, and requires want each time.
func swapFilter(t *testing.T, o codechttp.Options, in, want string) {
	t.Helper()
	run := func(chunks ...string) string {
		c := swapCodec(t, o)
		var out strings.Builder
		for _, chunk := range chunks {
			b, err := c.Filter(inspect.FromClient, []byte(chunk))
			if err != nil {
				t.Fatalf("Filter(%q): %v", chunk, err)
			}
			out.Write(b)
		}
		return out.String()
	}
	for i := 0; i <= len(in); i++ {
		if got := run(in[:i], in[i:]); got != want {
			t.Fatalf("split at %d:\n got %q\nwant %q", i, got, want)
		}
	}
	if got := run(bytewise(in)...); got != want {
		t.Fatalf("byte by byte:\n got %q\nwant %q", got, want)
	}
}

// A swapped request leaves with the upstream's bearer token in place of the
// caller's, whatever the split; every other field and the body stay
// byte-exact, and the caller's token never leaves.
func TestASwappedRequestCarriesTheUpstreamBearer(t *testing.T) {
	in := "POST /a HTTP/1.1\r\nHost: h\r\nauthorization: Bearer " + secret + "\r\nAccept: */*\r\nContent-Length: 2\r\n\r\nok" +
		"GET /b HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer " + secret + "\r\nX-Folded: a\r\n b\r\n\r\n"
	want := "POST /a HTTP/1.1\r\nHost: h\r\nAccept: */*\r\nContent-Length: 2\r\nAuthorization: Bearer " + upstreamToken + "\r\n" + via() + "\r\nok" +
		"GET /b HTTP/1.1\r\nHost: h\r\nX-Folded: a\r\n b\r\nAuthorization: Bearer " + upstreamToken + "\r\n" + via() + "\r\n"
	swapFilter(t, authSwap, in, want)
	if strings.Contains(want, secret) {
		t.Fatal("the expectation leaks the caller token")
	}
}

// A credential without Bearer goes out as Basic, user and password
// base64-encoded as RFC 7617 has it.
func TestASwappedRequestCarriesBasicWhenTheCredentialIsNotABearer(t *testing.T) {
	basic := func(_ context.Context, credential string) (codechttp.SwapResult, error) {
		return codechttp.SwapResult{User: "svc", Password: "p:ss"}, nil
	}
	in := "GET /x HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer " + secret + "\r\n\r\n"
	want := "GET /x HTTP/1.1\r\nHost: h\r\nAuthorization: Basic c3ZjOnA6c3M=\r\n" + via() + "\r\n"
	swapFilter(t, codechttp.Options{Swap: basic}, in, want)
}

// An impersonating lane names the verified caller and their groups, and
// drops any Impersonate-* the client wrote, which would otherwise ride the
// sidecar's credential. The swap also composes with an origin rewrite.
func TestAnImpersonatingLaneNamesTheCaller(t *testing.T) {
	in := "GET /x HTTP/1.1\r\nHost: 127.0.0.1:19999\r\nImpersonate-User: admin\r\nAuthorization: Bearer " + secret +
		"\r\nimpersonate-group: system:masters\r\nImpersonate-Uid: 1\r\nAccept: */*\r\n\r\n"
	want := "GET /x HTTP/1.1\r\nHost: api.example.com\r\nAccept: */*\r\n" +
		"X-Forwarded-Host: 127.0.0.1:19999\r\nX-Forwarded-Proto: http\r\n" +
		"Authorization: Bearer " + upstreamToken + "\r\n" +
		"Impersonate-User: alice@example.com\r\nImpersonate-Group: dev\r\nImpersonate-Group: ops\r\n" + via() + "\r\n"
	o := authSwap
	o.Impersonate, o.Origin = true, origin
	swapFilter(t, o, in, want)
}

// A swap lane that does not impersonate still drops every Impersonate-*
// the client wrote: with a credential that holds the impersonate verb, the
// header would let the caller act as anyone.
func TestASwapLaneDropsClientImpersonationWithoutImpersonate(t *testing.T) {
	in := "GET /x HTTP/1.1\r\nHost: h\r\nImpersonate-User: admin\r\nAuthorization: Bearer " + secret +
		"\r\nImpersonate-Group: system:masters\r\nimpersonate-uid: 1\r\nImpersonate-Extra-scopes: all\r\nAccept: */*\r\n\r\n"
	want := "GET /x HTTP/1.1\r\nHost: h\r\nAccept: */*\r\nAuthorization: Bearer " + upstreamToken + "\r\n" + via() + "\r\n"
	swapFilter(t, authSwap, in, want)
}

// Without an email the subject names the caller; with no identity at all
// the request runs as the credential and no Impersonate field is added.
func TestImpersonationFallsBackToTheSubject(t *testing.T) {
	for name, tc := range map[string]struct {
		id   session.Identity
		want string
	}{
		"subject": {session.Identity{Subject: "sa-1"}, "Impersonate-User: sa-1\r\n"},
		"nobody":  {session.Identity{}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			swap := func(context.Context, string) (codechttp.SwapResult, error) {
				return codechttp.SwapResult{Password: upstreamToken, Bearer: true, Identity: tc.id}, nil
			}
			in := "GET /x HTTP/1.1\r\nHost: h\r\n\r\n"
			want := "GET /x HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer " + upstreamToken + "\r\n" + tc.want + via() + "\r\n"
			swapFilter(t, codechttp.Options{Swap: swap, Impersonate: true}, in, want)
		})
	}
}

// A lane that lifts no credential header still swaps: the resolver is
// called with "" and the client's own Authorization is replaced.
func TestASwapLaneWithoutACredentialHeaderResolvesTheLanesCredential(t *testing.T) {
	var got atomic.Value
	swap := func(_ context.Context, credential string) (codechttp.SwapResult, error) {
		got.Store(credential)
		return codechttp.SwapResult{Password: upstreamToken, Bearer: true}, nil
	}
	in := "GET /x HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer client-own\r\n\r\n"
	want := "GET /x HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer " + upstreamToken + "\r\n" + via() + "\r\n"
	swapFilter(t, codechttp.Options{Swap: swap}, in, want)
	if v, _ := got.Load().(string); v != "" {
		t.Errorf("resolver got %q; want \"\"", v)
	}
}

// The resolver runs once per request, never once per connection: a pooled
// connection carries many callers.
func TestTheSwapResolvesEveryRequest(t *testing.T) {
	var calls atomic.Int32
	swap := func(ctx context.Context, credential string) (codechttp.SwapResult, error) {
		calls.Add(1)
		return swapSecret(ctx, credential)
	}
	c := swapCodec(t, codechttp.Options{Swap: swap, CredentialHeader: "Authorization", AuthenticatesCaller: true})
	req := "GET /x HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer " + secret + "\r\n\r\n"
	if _, err := c.Filter(inspect.FromClient, []byte(req+req+req)); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("resolver ran %d times for 3 requests", n)
	}
}

// A refused swap is a CredentialRefusal: nothing of the request goes out,
// the error carries the request line for the audit row and never the
// token, and the connection is over.
func TestARefusedSwapForwardsNothing(t *testing.T) {
	refuse := func(context.Context, string) (codechttp.SwapResult, error) {
		return codechttp.SwapResult{}, fmt.Errorf("%w: no credential is granted to this caller", codectypes.ErrLoginRefused)
	}
	c := swapCodec(t, codechttp.Options{Swap: refuse, CredentialHeader: "Authorization", AuthenticatesCaller: true})
	out, err := c.Filter(inspect.FromClient, []byte("DELETE /items/1 HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer "+secret+"\r\n\r\n"))
	var refusal *inspect.CredentialRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Filter = %q, %v; want a CredentialRefusal", out, err)
	}
	if len(out) != 0 {
		t.Errorf("forwarded %q before refusing", out)
	}
	if refusal.Request != "DELETE /items/1" || strings.Contains(err.Error(), secret) {
		t.Errorf("refusal = %q for %q", err, refusal.Request)
	}
	if !errors.Is(err, codectypes.ErrLoginRefused) {
		t.Error("the refusal lost ErrLoginRefused")
	}
	if _, err := c.Filter(inspect.FromClient, []byte("GET / HTTP/1.1\r\n\r\n")); err == nil {
		t.Error("the connection went on after a refusal")
	}
}

// The caller check runs first: a stranger is refused before the resolver
// sees anything, so the lane's credential source is never asked for them.
func TestTheCallerCheckRunsBeforeTheSwap(t *testing.T) {
	var called atomic.Bool
	swap := func(ctx context.Context, credential string) (codechttp.SwapResult, error) {
		called.Store(true)
		return swapSecret(ctx, credential)
	}
	c := swapCodec(t, codechttp.Options{Swap: swap, CredentialHeader: "Authorization", AuthenticatesCaller: true})
	_, err := c.Filter(inspect.FromClient, []byte("GET /x HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer guess\r\n\r\n"))
	var refusal *inspect.CredentialRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Filter: %v; want a CredentialRefusal", err)
	}
	if called.Load() {
		t.Error("the resolver ran for a refused caller")
	}
}

// A resolver failure that is not a refusal (the source is down) is not an
// identity refusal: it ends the connection through the filter's own error,
// without forwarding the request under the client's credential.
func TestAFailedSwapIsNotARefusal(t *testing.T) {
	down := func(context.Context, string) (codechttp.SwapResult, error) {
		return codechttp.SwapResult{}, errors.New("metadata server unreachable")
	}
	c := swapCodec(t, codechttp.Options{Swap: down})
	out, err := c.Filter(inspect.FromClient, []byte("GET /x HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer own\r\n\r\n"))
	var refusal *inspect.CredentialRefusal
	if err == nil || errors.As(err, &refusal) {
		t.Fatalf("Filter = %q, %v; want a plain error", out, err)
	}
	if len(out) != 0 || !strings.Contains(err.Error(), "metadata server unreachable") {
		t.Errorf("Filter = %q, %v", out, err)
	}
}

// A swapping lane is strict: a head it cannot frame is refused rather than
// passed through with the client's own Authorization.
func TestASwapLaneRefusesWhatItCannotFrame(t *testing.T) {
	c := swapCodec(t, codechttp.Options{Swap: swapSecret})
	out, err := c.Filter(inspect.FromClient, []byte("not a request line\r\nAuthorization: Bearer own\r\n\r\n"))
	if err == nil || len(out) != 0 || !strings.Contains(err.Error(), "replaces the credential of every request") {
		t.Fatalf("Filter = %q, %v", out, err)
	}
}

// A value with a line break would write a field of its own: refused.
func TestASwapValueWithALineBreakIsRefused(t *testing.T) {
	swap := func(context.Context, string) (codechttp.SwapResult, error) {
		return codechttp.SwapResult{Password: upstreamToken, Bearer: true,
			Identity: session.Identity{Subject: "a", Groups: []string{"g\r\nX-Injected: 1"}}}, nil
	}
	c := swapCodec(t, codechttp.Options{Swap: swap, Impersonate: true})
	out, err := c.Filter(inspect.FromClient, []byte("GET /x HTTP/1.1\r\nHost: h\r\n\r\n"))
	if err == nil || len(out) != 0 {
		t.Fatalf("Filter = %q, %v", out, err)
	}
}

// Impersonate without a Swap has no identity to impersonate: a programmer
// error, caught at construction.
func TestImpersonateNeedsASwap(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New accepted Impersonate with no Swap")
		}
	}()
	codechttp.New(codechttp.Options{Impersonate: true})
}
