package http_test

import (
	"errors"
	"strings"
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
)

const secret = "s3cr3t-caller-token"

var errNotListed = errors.New("the caller token is not accepted")

// acceptSecret is the check a gate would install: secret only.
func acceptSecret(credential string) error {
	if credential != secret {
		return errNotListed
	}
	return nil
}

// authCodec is a codec on a lane that authenticates its caller, with
// acceptSecret installed as the gate would install its own.
func authCodec(t *testing.T, o *codechttp.Origin) *codechttp.Codec {
	t.Helper()
	c := codechttp.New(codechttp.Options{CredentialHeader: "X-Hoop-Token", AuthenticatesCaller: true, Origin: o})
	if err := c.AuthenticateCallers(acceptSecret); err != nil {
		t.Fatal(err)
	}
	return c
}

// authFilter runs in through a fresh authenticating codec, split at every
// point and byte by byte, and requires want each time.
func authFilter(t *testing.T, o *codechttp.Origin, in, want string) {
	t.Helper()
	run := func(chunks ...string) string {
		c := authCodec(t, o)
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

// An admitted request leaves without its token: every line of it, in any
// case and with a folded continuation, while every other field keeps its
// place and its bytes. Bodies pass byte-exact.
func TestAnAdmittedRequestLeavesWithoutItsToken(t *testing.T) {
	in := "POST /a HTTP/1.1\r\nHost: h\r\nx-hoop-token: " + secret + "\r\nAccept: */*\r\nContent-Length: 2\r\n\r\nok" +
		"GET /b HTTP/1.1\r\nHost: h\r\nX-Hoop-Token: " + secret + "\r\nX-Folded: a\r\n b\r\n\r\n"
	want := "POST /a HTTP/1.1\r\nHost: h\r\nAccept: */*\r\nContent-Length: 2\r\n" + via() + "\r\nok" +
		"GET /b HTTP/1.1\r\nHost: h\r\nX-Folded: a\r\n b\r\n" + via() + "\r\n"
	authFilter(t, nil, in, want)
}

// On a lane that points every request at an origin, the check, the strip
// and the Host rewrite apply to the same head.
func TestAnAdmittedRequestOnAnOriginLane(t *testing.T) {
	in := "GET /x HTTP/1.1\r\nHost: 127.0.0.1:19999\r\nX-Hoop-Token: " + secret + "\r\nAccept: */*\r\n\r\n"
	want := "GET /x HTTP/1.1\r\nHost: api.example.com\r\nAccept: */*\r\n" +
		"X-Forwarded-Host: 127.0.0.1:19999\r\nX-Forwarded-Proto: http\r\n" + via() + "\r\n"
	authFilter(t, origin, in, want)
}

// The gate judges a request only once its body has arrived. The filter
// refuses a stranger's head before then, whether its body ever comes, so
// the sidecar never holds a body for them: no header,
// a wrong token, a token repeated (joined "a, b" as the gate would see it),
// a token folded onto a second line.
func TestARefusedHeadForwardsNothing(t *testing.T) {
	for name, in := range map[string]string{
		"no header":        "DELETE /items/1 HTTP/1.1\r\nHost: h\r\nContent-Length: 1\r\n\r\n",
		"wrong token":      "DELETE /items/1 HTTP/1.1\r\nHost: h\r\nX-Hoop-Token: guess\r\nContent-Length: 1\r\n\r\n",
		"chunked, no body": "POST /items HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n",
		"repeated":         "GET /x HTTP/1.1\r\nX-Hoop-Token: " + secret + "\r\nX-Hoop-Token: " + secret + "\r\n\r\n",
		"folded":           "GET /x HTTP/1.1\r\nX-Hoop-Token: " + secret + "\r\n more\r\n\r\n",
		// Behind an admitted request in the same read: that one goes out,
		// this one does not.
		"pipelined": "GET /ok HTTP/1.1\r\nX-Hoop-Token: " + secret + "\r\n\r\n" +
			"DELETE /items/1 HTTP/1.1\r\nHost: h\r\nContent-Length: 1\r\n\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			out, err := authCodec(t, nil).Filter(inspect.FromClient, []byte(in))
			var refusal *inspect.CredentialRefusal
			if !errors.As(err, &refusal) {
				t.Fatalf("Filter = %q, %v; want a CredentialRefusal", out, err)
			}
			if len(out) != 0 {
				t.Errorf("forwarded %q before refusing", out)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the refusal carries the token: %q", err)
			}
			if !strings.HasPrefix(err.Error(), "request refused: ") || refusal.Request == "" {
				t.Errorf("refusal = %q for %q; want a 403 body and the request line", err, refusal.Request)
			}
		})
	}
}

// A caller the lane refuses is a stranger whose token it does not know, so
// the identity row collapses the segments shaped like one instead of
// redacting them.
func TestAnIdentityRefusalRowCollapsesATokenInThePath(t *testing.T) {
	_, err := authCodec(t, nil).Filter(inspect.FromClient,
		[]byte("GET /pods/Unknown-Token-0123456789abcdef/log HTTP/1.1\r\n\r\n"))
	var refusal *inspect.CredentialRefusal
	if !errors.As(err, &refusal) || refusal.Request != "GET /pods/*/log" {
		t.Fatalf("Filter = %v; want an identity refusal of GET /pods/*/log", err)
	}
}

// A codec built to authenticate but given no check refuses everything: it
// cannot tell a caller from a stranger.
func TestAnAuthenticatingCodecWithoutACheckForwardsNothing(t *testing.T) {
	c := codechttp.New(codechttp.Options{CredentialHeader: "x-hoop-token", AuthenticatesCaller: true})
	if out, err := c.Filter(inspect.FromClient, []byte("GET / HTTP/1.1\r\nX-Hoop-Token: "+secret+"\r\n\r\n")); err == nil {
		t.Fatalf("forwarded %q", out)
	}
}

// A codec that does not authenticate refuses the check, so a gate that
// believes it installed one finds out at once.
func TestOnlyAnAuthenticatingCodecTakesACheck(t *testing.T) {
	c := codechttp.New(codechttp.Options{CredentialHeader: "x-user"})
	if err := c.AuthenticateCallers(acceptSecret); err == nil {
		t.Error("a codec that never runs the check accepted one")
	}
}

// An authenticating lane cannot pass through what it cannot frame, as a
// Via-only lane does: nothing checked it. It refuses instead, and the error
// says why without quoting the head, where net/http's own error would.
func TestAnAuthenticatingLaneRefusesWhatItCannotFrame(t *testing.T) {
	for name, in := range map[string]string{
		"h2 preface":     "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\nX-Hoop-Token: " + secret + "\r\n",
		"garbage":        "not a request line\r\nX-Hoop-Token: " + secret + "\r\n\r\n",
		"oversized head": "GET / HTTP/1.1\r\nX-Hoop-Token: " + secret + "\r\nX-Pad: " + strings.Repeat("a", 1<<20+1),
		// A lone CR is not a line break to net/http; it must not become
		// one that hides the token from the strip either.
		"lone CR": "GET / HTTP/1.1\r\nHost: h\r\nX-Other: a\rX-Hoop-Token: " + secret + "\r\n\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			out, err := authCodec(t, nil).Filter(inspect.FromClient, []byte(in))
			if err == nil {
				t.Fatalf("forwarded %q instead of refusing", out)
			}
			if strings.Contains(string(out), secret) || strings.Contains(err.Error(), secret) {
				t.Fatalf("the token leaked: out %q, err %q", out, err)
			}
			if !strings.Contains(err.Error(), "removes the x-hoop-token header") {
				t.Errorf("error %q does not say what the lane promises", err)
			}
		})
	}
}

// A refused Upgrade switches nothing, so the next request on the connection
// is still checked and stripped; a Via-only lane would have stopped
// rewriting at the Upgrade.
func TestAnAuthenticatingLaneKeepsCheckingAfterARefusedUpgrade(t *testing.T) {
	c := authCodec(t, nil)
	head := "GET / HTTP/1.1\r\nHost: h\r\nX-Hoop-Token: " + secret + "\r\nUpgrade: bogus\r\nConnection: Upgrade\r\n\r\n"
	if out, err := c.Filter(inspect.FromClient, []byte(head)); err != nil || strings.Contains(string(out), secret) {
		t.Fatalf("upgrade request: %q, %v", out, err)
	}
	if _, _, err := c.Decode(inspect.FromServer, []byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")); err != nil {
		t.Fatal(err)
	}
	if out, err := c.Filter(inspect.FromClient, []byte("GET /next HTTP/1.1\r\nHost: h\r\n\r\n")); err == nil {
		t.Errorf("a token-less request after a refused upgrade went out as %q", out)
	}
}

// Bytes sent before the server answered a switch request are refused:
// passed through they would skip the check if the server declines.
func TestAnAuthenticatingLaneRefusesBytesBeforeTheSwitchIsAnswered(t *testing.T) {
	c := authCodec(t, nil)
	head := "GET /ws HTTP/1.1\r\nHost: h\r\nX-Hoop-Token: " + secret + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"
	if _, err := c.Filter(inspect.FromClient, []byte(head)); err != nil {
		t.Fatal(err)
	}
	if out, err := c.Filter(inspect.FromClient, []byte("GET /next HTTP/1.1\r\n\r\n")); err == nil {
		t.Errorf("bytes before the answer went out as %q", out)
	}
}

// filterThenDecode runs client bytes the way the gate does: the filter
// first, then the decoder over what the filter forwards.
func filterThenDecode(t *testing.T, c *codechttp.Codec, in string) []inspect.Statement {
	t.Helper()
	out, err := c.Filter(inspect.FromClient, []byte(in))
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	stmts, _, err := c.Decode(inspect.FromClient, out)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return stmts
}

// The gate decodes what the filter forwards, which no longer carries the
// token, so the filter hands each admitted request's value to the codec in
// order, for the gate's own check.
func TestAnAdmittedRequestKeepsItsCredentialForTheGate(t *testing.T) {
	c := codechttp.New(codechttp.Options{CredentialHeader: "x-hoop-token", AuthenticatesCaller: true})
	if err := c.AuthenticateCallers(func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	stmts := filterThenDecode(t, c,
		"GET /a HTTP/1.1\r\nHost: h\r\nX-Hoop-Token: first\r\n\r\n"+
			"GET /b HTTP/1.1\r\nHost: h\r\nX-Hoop-Token: second\r\n\r\n")
	if len(stmts) != 2 {
		t.Fatalf("decoded %d statements, want 2", len(stmts))
	}
	for i, want := range []string{"first", "second"} {
		if cred, ok := c.TakeCredential(&stmts[i]); !ok || cred != want {
			t.Errorf("request %d: TakeCredential = %q, %v; want %q", i, cred, ok, want)
		}
	}
}

// A request that reaches the decoder without passing the filter was never
// checked. The stream is refused rather than given a guessed credential.
func TestAnAuthenticatingCodecRefusesARequestTheFilterNeverSaw(t *testing.T) {
	_, _, err := authCodec(t, nil).Decode(inspect.FromClient,
		[]byte("GET /x HTTP/1.1\r\nHost: h\r\nX-Hoop-Token: "+secret+"\r\n\r\n"))
	if !errors.Is(err, inspect.ErrStreamUnsafe) {
		t.Fatalf("Decode error = %v, want ErrStreamUnsafe", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the refusal carries the token: %v", err)
	}
}

// The token is kept out of the captured headers even when the lane's own
// allowlist names it.
func TestTheTokenStaysOutOfCapturedHeaders(t *testing.T) {
	c := codechttp.New(codechttp.Options{
		Headers:             []string{"X-Hoop-Token", "Accept"},
		CredentialHeader:    "x-hoop-token",
		AuthenticatesCaller: true,
	})
	if err := c.AuthenticateCallers(acceptSecret); err != nil {
		t.Fatal(err)
	}
	stmts := filterThenDecode(t, c, "GET /x HTTP/1.1\r\nHost: h\r\nAccept: */*\r\nX-Hoop-Token: "+secret+"\r\n\r\n")
	if len(stmts) != 1 {
		t.Fatalf("decoded %d statements, want 1", len(stmts))
	}
	if _, ok := stmts[0].HTTP.Headers["x-hoop-token"]; ok {
		t.Errorf("captured headers keep the token: %v", stmts[0].HTTP.Headers)
	}
	if stmts[0].HTTP.Headers["accept"] == "" {
		t.Errorf("the other allowlisted header was lost: %v", stmts[0].HTTP.Headers)
	}
}

// A lane with nothing to check would forward every caller; it is a
// programmer error, caught at construction.
func TestAuthenticatesCallerNeedsACredentialHeader(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New accepted AuthenticatesCaller with no CredentialHeader")
		}
	}()
	codechttp.New(codechttp.Options{AuthenticatesCaller: true})
}
