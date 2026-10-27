package http_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/upstreamcred"
	"github.com/hoophq/hoop/sidecar/upstreamcred/upstreamcredtest"
)

// upstreamToken is the token the lane holds, never the client.
const upstreamToken = upstreamcredtest.Token

// holdingCodec is a caller-token codec on an origin lane that holds the
// upstream's Authorization, as the AgentCore template's lane is built.
func holdingCodec(t *testing.T, cred *upstreamcred.Credential, seen *codechttp.SeenCredentials) *codechttp.Codec {
	t.Helper()
	c := codechttp.New(codechttp.Options{CredentialHeader: "X-Hoop-Token", AuthenticatesCaller: true, Origin: origin, Seen: seen})
	if err := c.AuthenticateCallers(acceptSecret); err != nil {
		t.Fatal(err)
	}
	if err := c.HoldUpstreamCredential(cred); err != nil {
		t.Fatal(err)
	}
	return c
}

func filterAll(t *testing.T, c *codechttp.Codec, chunks ...string) (string, error) {
	t.Helper()
	var out strings.Builder
	for _, chunk := range chunks {
		b, err := c.Filter(inspect.FromClient, []byte(chunk))
		if err != nil {
			return out.String(), err
		}
		out.Write(b)
	}
	return out.String(), nil
}

// Every request of a kept-alive connection leaves with exactly the lane's
// credential: whatever the client sent in its place, none, one, or two in
// mixed case, is gone, a chunked body and a WebSocket handshake included.
// The head is split at every point and fed byte by byte, as reads arrive.
func TestTheLaneSendsItsCredentialInPlaceOfTheClients(t *testing.T) {
	cred := upstreamcredtest.Open(t, "Authorization", "Bearer")
	caller := "X-Hoop-Token: " + secret + "\r\n"
	in := "POST /a HTTP/1.1\r\nHost: h\r\n" + caller + "Content-Length: 2\r\n\r\nok" +
		"GET /b HTTP/1.1\r\nHost: h\r\nauthorization: Bearer agent-placeholder\r\n" + caller + "\r\n" +
		"POST /c HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer first\r\n" + caller +
		"AUTHORIZATION: Basic second\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nok\r\n0\r\n\r\n" +
		"GET /ws HTTP/1.1\r\nHost: h\r\n" + caller + "Connection: Upgrade\r\nUpgrade: websocket\r\n\r\n"
	fwd := "X-Forwarded-Host: h\r\nX-Forwarded-Proto: http\r\nAuthorization: Bearer " + upstreamToken + "\r\n" + via()
	want := "POST /a HTTP/1.1\r\nHost: api.example.com\r\nContent-Length: 2\r\n" + fwd + "\r\nok" +
		"GET /b HTTP/1.1\r\nHost: api.example.com\r\n" + fwd + "\r\n" +
		"POST /c HTTP/1.1\r\nHost: api.example.com\r\nTransfer-Encoding: chunked\r\n" + fwd + "\r\n2\r\nok\r\n0\r\n\r\n" +
		"GET /ws HTTP/1.1\r\nHost: api.example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n" + fwd + "\r\n"
	for i := 0; i <= len(in); i++ {
		got, err := filterAll(t, holdingCodec(t, cred, nil), in[:i], in[i:])
		if err != nil || got != want {
			t.Fatalf("split at %d: err %v\n got %q\nwant %q", i, err, got, want)
		}
	}
	if got, err := filterAll(t, holdingCodec(t, cred, nil), bytewise(in)...); err != nil || got != want {
		t.Fatalf("byte by byte: err %v\n got %q\nwant %q", err, got, want)
	}
}

// An API key with no scheme goes alone, under the header as configured.
func TestTheLaneSendsATokenWithoutAScheme(t *testing.T) {
	c := plainCodec()
	if err := c.HoldUpstreamCredential(upstreamcredtest.Open(t, "X-API-Key", "")); err != nil {
		t.Fatal(err)
	}
	got, err := filterAll(t, c, "GET / HTTP/1.1\r\nHost: h\r\nx-api-key: agent-copy\r\n\r\n")
	if want := "GET / HTTP/1.1\r\nHost: h\r\nX-API-Key: " + upstreamToken + "\r\n" + via() + "\r\n"; err != nil || got != want {
		t.Fatalf("err %v\n got %q\nwant %q", err, got, want)
	}
}

// The gate decodes what the filter forwards, which carries the lane's
// token. The codec captures only allowlisted headers, so no statement
// holds it, even on a lane that captures other headers.
func TestTheLanesTokenIsInNoStatement(t *testing.T) {
	c := codechttp.New(codechttp.Options{Headers: []string{"Accept"}, CaptureBody: true})
	if err := c.HoldUpstreamCredential(upstreamcredtest.Open(t, "Authorization", "Bearer")); err != nil {
		t.Fatal(err)
	}
	stmts := filterThenDecode(t, c, "GET /a HTTP/1.1\r\nHost: h\r\nAccept: */*\r\n\r\n")
	if len(stmts) != 1 || stmts[0].HTTP == nil || stmts[0].HTTP.Headers["accept"] != "*/*" {
		t.Fatalf("statements = %+v, want one capturing Accept", stmts)
	}
	if v, ok := stmts[0].HTTP.Headers["authorization"]; ok {
		t.Errorf("the statement captured Authorization %q", v)
	}
	if strings.Contains(stmts[0].Text, upstreamToken) || strings.Contains(stmts[0].HTTP.Target, upstreamToken) {
		t.Errorf("the statement holds the token: %+v", stmts[0])
	}
}

// A lane that holds a credential is strict: a head it cannot parse would
// otherwise leave as written, without the lane's credential and with the
// client's copy. The refusal names the header, never the token.
func TestALaneHoldingACredentialRefusesAHeadItCannotParse(t *testing.T) {
	for name, in := range map[string]string{
		"no colon":     "GET / HTTP/1.1\r\nHost h\r\nAuthorization: Bearer agent-copy\r\n\r\n",
		"http/2":       "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n",
		"bad protocol": "GET / HTTP/9.9\r\n\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			c := plainCodec()
			if err := c.HoldUpstreamCredential(upstreamcredtest.Open(t, "Authorization", "Bearer")); err != nil {
				t.Fatal(err)
			}
			out, err := filterAll(t, c, in)
			if err == nil || out != "" {
				t.Fatalf("forwarded %q, err %v; want a refusal and nothing forwarded", out, err)
			}
			if !strings.Contains(err.Error(), "sets its own Authorization header") || strings.Contains(err.Error(), upstreamToken) {
				t.Errorf("refusal = %q, want one naming the header and not the token", err)
			}
		})
	}
}

// A client that has the lane's token, from an upstream that echoed it or
// any other way, cannot send it anywhere but its header: the request is
// refused before any of it reaches the upstream, and the row holds no
// token.
func TestTheLanesTokenOutsideItsHeaderIsRefused(t *testing.T) {
	cred := upstreamcredtest.Open(t, "X-API-Key", "")
	build := func() *codechttp.Codec {
		c := plainCodec()
		if err := c.HoldUpstreamCredential(cred); err != nil {
			t.Fatal(err)
		}
		return c
	}
	for name, tc := range map[string]struct{ in, where string }{
		"query":        {"GET /a?k=" + upstreamToken + " HTTP/1.1\r\nHost: h\r\n\r\n", "request line"},
		"path":         {"GET /a/" + upstreamToken + " HTTP/1.1\r\nHost: h\r\n\r\n", "request line"},
		"other header": {"GET /a HTTP/1.1\r\nHost: h\r\nX-Note: " + upstreamToken + "\r\n\r\n", "header X-Note"},
		"body": {"POST /a HTTP/1.1\r\nHost: h\r\nContent-Length: " + strconv.Itoa(len(upstreamToken)) + "\r\n\r\n" +
			upstreamToken, "body"},
	} {
		t.Run(name, func(t *testing.T) {
			for _, chunks := range [][]string{{tc.in}, bytewise(tc.in)} {
				_, err := filterAll(t, build(), chunks...)
				var refusal *inspect.PlacementRefusal
				if !errors.As(err, &refusal) || refusal.Where != tc.where {
					t.Fatalf("Filter = %v; want a PlacementRefusal at %q", err, tc.where)
				}
				if strings.Contains(err.Error(), upstreamToken) || strings.Contains(refusal.Request, upstreamToken) {
					t.Errorf("the refusal carries the token: %q, row %q", err, refusal.Request)
				}
			}
		})
	}
	// Its own header is its home; the copy there is replaced, not refused.
	if _, err := filterAll(t, build(), "GET /a HTTP/1.1\r\nHost: h\r\nX-API-Key: "+upstreamToken+"\r\n\r\n"); err != nil {
		t.Errorf("the token in its own header was refused: %v", err)
	}
}

// The upstream never reads the Authorization a client sends on a lane that
// replaces it, so a 2xx must not teach the lane that value as a credential
// the upstream accepted: it would then refuse the client's own placeholder
// in a later URL. The lane's token is what the upstream accepted.
func TestTheLaneRemembersItsOwnTokenNotTheClientsCopy(t *testing.T) {
	const placeholder = "agent-placeholder-0123456789"
	cred := upstreamcredtest.Open(t, "Authorization", "Bearer")
	seen := codechttp.NewSeenCredentials()
	caller := "X-Hoop-Token: " + secret + "\r\n"

	c := holdingCodec(t, cred, seen)
	admit(t, c, "GET /a HTTP/1.1\r\nHost: h\r\n"+caller+"Authorization: Bearer "+placeholder+"\r\n\r\n")
	answer(t, c, "200 OK")

	admit(t, holdingCodec(t, cred, seen), "GET /a/"+placeholder+" HTTP/1.1\r\nHost: h\r\n"+caller+"\r\n")
	if _, err := filterAll(t, holdingCodec(t, cred, seen), "GET /a/"+upstreamToken+" HTTP/1.1\r\nHost: h\r\n"+caller+"\r\n"); err == nil {
		t.Error("the lane's token in a path was forwarded")
	}
}

// The codec lifts the caller's credential from what the filter forwards,
// so a credential in that header would reach the identity resolver as the
// caller's. daemon refuses the configuration; the codec refuses it too.
func TestACodecRefusesACredentialInTheCallerHeader(t *testing.T) {
	c := codechttp.New(codechttp.Options{CredentialHeader: "authorization"})
	if err := c.HoldUpstreamCredential(upstreamcredtest.Open(t, "Authorization", "Bearer")); err == nil {
		t.Fatal("a codec that lifts Authorization took an Authorization credential")
	}
	if err := plainCodec().HoldUpstreamCredential(nil); err == nil {
		t.Fatal("a codec took a nil credential")
	}
}

// A client's copy of the lane's header is dropped wherever an upstream may
// read it: as a chunked trailer, which a non-compliant upstream merges into
// the header section, and spelled with _ for -, which a CGI-style server
// reads as the same name. Fed whole and byte by byte.
func TestTheLaneDropsTheClientsCopyFromTrailersAndUnderscoreSpellings(t *testing.T) {
	cred := upstreamcredtest.Open(t, "X-API-Key", "")
	build := func() *codechttp.Codec {
		c := plainCodec()
		if err := c.HoldUpstreamCredential(cred); err != nil {
			t.Fatal(err)
		}
		return c
	}
	in := "POST /a HTTP/1.1\r\nHost: h\r\nX_API_Key: agent-copy\r\nTrailer: X-Api-Key\r\nTransfer-Encoding: chunked\r\n\r\n" +
		"2\r\nok\r\n0\r\nx-api-key: agent-copy\r\nX-Other: kept\r\nX_Api_Key : again\r\n\r\n" +
		"GET /b HTTP/1.1\r\nHost: h\r\n\r\n"
	sent := "X-API-Key: " + upstreamToken + "\r\n" + via()
	want := "POST /a HTTP/1.1\r\nHost: h\r\nTrailer: X-Api-Key\r\nTransfer-Encoding: chunked\r\n" + sent + "\r\n" +
		"2\r\nok\r\n0\r\nX-Other: kept\r\n\r\n" +
		"GET /b HTTP/1.1\r\nHost: h\r\n" + sent + "\r\n"
	for _, chunks := range [][]string{{in}, bytewise(in)} {
		if got, err := filterAll(t, build(), chunks...); err != nil || got != want {
			t.Fatalf("%d chunks: err %v\n got %q\nwant %q", len(chunks), err, got, want)
		}
	}
	// A lane that holds no credential streams its trailers as they come.
	plain := "POST /a HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nX-API-Key: theirs\r\n\r\n"
	got, err := filterAll(t, plainCodec(), plain)
	if wantPlain := strings.Replace(plain, "\r\n\r\n", "\r\n"+via()+"\r\n", 1); err != nil || got != wantPlain {
		t.Fatalf("plain lane: err %v\n got %q\nwant %q", err, got, wantPlain)
	}
}
