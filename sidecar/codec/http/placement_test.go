package http_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// bearer is an upstream's token, long enough to be looked for.
const bearer = "kube-sa-token-0123456789abcdef"

// mustRefuse runs in through fresh codecs from build, whole and byte
// by byte, and requires a PlacementRefusal naming where each time, with no
// token in its text or its row.
func mustRefuse(t *testing.T, build func() *codechttp.Codec, in, where string) *inspect.PlacementRefusal {
	t.Helper()
	var last *inspect.PlacementRefusal
	for _, chunks := range [][]string{{in}, bytewise(in)} {
		c := build()
		var err error
		for _, chunk := range chunks {
			if _, err = c.Filter(inspect.FromClient, []byte(chunk)); err != nil {
				break
			}
		}
		var refusal *inspect.PlacementRefusal
		if !errors.As(err, &refusal) {
			t.Fatalf("Filter in %d chunks = %v; want a PlacementRefusal", len(chunks), err)
		}
		if refusal.Where != where {
			t.Errorf("Where = %q, want %q", refusal.Where, where)
		}
		for _, tok := range []string{secret, bearer} {
			if strings.Contains(err.Error(), tok) || strings.Contains(refusal.Request, tok) {
				t.Errorf("the refusal carries a token: %q, row %q", err, refusal.Request)
			}
		}
		if !strings.HasPrefix(err.Error(), "request refused: ") {
			t.Errorf("refusal = %q, want a 403 body", err)
		}
		last = refusal
	}
	return last
}

func plainCodec() *codechttp.Codec { return codechttp.New(codechttp.Options{}) }

// A credential the lane knows is refused anywhere but its own header: the
// caller token on a lane that admits by it, and the request's own
// Authorization credential on any lane. The vault fills both wherever the
// agent writes the placeholder.
func TestAKnownCredentialOutsideItsHeaderIsRefused(t *testing.T) {
	caller := func() *codechttp.Codec { return authCodec(t, nil) }
	withCaller := "X-Hoop-Token: " + secret + "\r\n"
	withBearer := "Authorization: Bearer " + bearer + "\r\n"
	for name, tc := range map[string]struct {
		build func() *codechttp.Codec
		in    string
		where string
	}{
		"caller token in the query": {caller,
			"GET /echo?t=" + secret + " HTTP/1.1\r\nHost: h\r\n" + withCaller + "\r\n", "request line"},
		"caller token in the path": {caller,
			"GET /pods/" + secret + " HTTP/1.1\r\nHost: h\r\n" + withCaller + "\r\n", "request line"},
		"caller token percent-encoded": {caller,
			"GET /echo?t=" + strings.ReplaceAll(secret, "-", "%2D") + " HTTP/1.1\r\nHost: h\r\n" + withCaller + "\r\n", "request line"},
		"caller token in Authorization": {caller,
			"GET / HTTP/1.1\r\nHost: h\r\n" + withCaller + "Authorization: Bearer " + secret + "\r\n\r\n", "header Authorization"},
		"caller token in a form body": {caller,
			"POST /echo HTTP/1.1\r\nHost: h\r\n" + withCaller + "Content-Length: 21\r\n\r\nt=" + secret, "body"},
		"bearer in the path": {plainCodec,
			"GET /api/v1/namespaces/default/pods/" + bearer + " HTTP/1.1\r\nHost: h\r\n" + withBearer + "\r\n", "request line"},
		"bearer in another header": {plainCodec,
			"GET / HTTP/1.1\r\nHost: h\r\n" + withBearer + "X-Echo: " + bearer + "\r\n\r\n", "header X-Echo"},
		"bearer as a header name": {plainCodec,
			"GET / HTTP/1.1\r\nHost: h\r\n" + withBearer + bearer + ": x\r\n\r\n", "header name"},
		"bearer in a folded line": {plainCodec,
			"GET / HTTP/1.1\r\nHost: h\r\n" + withBearer + "X-Folded: a\r\n " + bearer + "\r\n\r\n", "header X-Folded"},
		"bearer in the second Authorization": {plainCodec,
			"POST / HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer other-token-0123456789\r\n" + withBearer +
				"Content-Length: 30\r\n\r\n" + bearer, "body"},
		"bearer across chunks": {plainCodec,
			"POST / HTTP/1.1\r\nHost: h\r\n" + withBearer + "Transfer-Encoding: chunked\r\n\r\n" +
				"a\r\n" + bearer[:10] + "\r\n14\r\n" + bearer[10:] + "\r\n0\r\n\r\n", "body"},
		"bearer in a chunk extension": {plainCodec,
			"POST / HTTP/1.1\r\nHost: h\r\n" + withBearer + "Transfer-Encoding: chunked\r\n\r\n" +
				"2;x=" + bearer + "\r\nok\r\n0\r\n\r\n", "body"},
		"bearer in a trailer": {plainCodec,
			"POST / HTTP/1.1\r\nHost: h\r\n" + withBearer + "Transfer-Encoding: chunked\r\n\r\n" +
				"2\r\nok\r\n0\r\nX-Trailer: " + bearer + "\r\n\r\n", "trailer"},
		"tab after the scheme": {plainCodec,
			"GET /?k=" + bearer + " HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer\t" + bearer + "\r\n\r\n", "request line"},
		"scheme-less Authorization": {plainCodec,
			"GET /?k=" + bearer + " HTTP/1.1\r\nHost: h\r\nAuthorization: " + bearer + "\r\n\r\n", "request line"},
	} {
		t.Run(name, func(t *testing.T) { mustRefuse(t, tc.build, tc.in, tc.where) })
	}
}

// In its own header a credential passes as before, and a value too short
// to look for is not looked for: a refusal on a coincidence is an outage.
func TestACredentialInItsOwnHeaderPasses(t *testing.T) {
	in := "POST /a HTTP/1.1\r\nHost: h\r\nX-Hoop-Token: " + secret + "\r\nAuthorization: Bearer " + bearer +
		"\r\nContent-Length: 2\r\n\r\nok"
	want := "POST /a HTTP/1.1\r\nHost: h\r\nAuthorization: Bearer " + bearer + "\r\nContent-Length: 2\r\n" + via() + "\r\nok"
	authFilter(t, nil, in, want)

	short := "POST /short-key HTTP/1.1\r\nAuthorization: Bearer short-key\r\nContent-Length: 9\r\n\r\nshort-key"
	if got := filterChunks(t, short); !strings.HasSuffix(got, "short-key") {
		t.Fatalf("a short credential was refused: %q", got)
	}
}

// The refusal row names the path with each known credential replaced, and
// the whole path when one survives only encoded. Never the query.
func TestARefusalRowRedactsThePath(t *testing.T) {
	r := mustRefuse(t, plainCodec,
		"GET /pods/"+bearer+"/log?t="+bearer+" HTTP/1.1\r\nAuthorization: Bearer "+bearer+"\r\n\r\n", "request line")
	if r.Request != "GET /pods/[redacted]/log" {
		t.Errorf("row = %q", r.Request)
	}
	encoded := strings.ReplaceAll(bearer, "-", "%2D")
	r = mustRefuse(t, plainCodec,
		"GET /pods/"+encoded+" HTTP/1.1\r\nAuthorization: Bearer "+bearer+"\r\n\r\n", "request line")
	if r.Request != "GET [redacted]" {
		t.Errorf("row = %q", r.Request)
	}
}

// answer feeds c one response, as the gate decodes each before relaying it.
func answer(t *testing.T, c *codechttp.Codec, status string) {
	t.Helper()
	if _, _, err := c.Decode(inspect.FromServer, []byte("HTTP/1.1 "+status+"\r\nContent-Length: 0\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
}

// admit runs in through c's filter and requires it to pass.
func admit(t *testing.T, c *codechttp.Codec, in string) {
	t.Helper()
	if _, err := c.Filter(inspect.FromClient, []byte(in)); err != nil {
		t.Fatalf("Filter(%q): %v", in, err)
	}
}

// sharedLane returns a factory whose codecs share one memory, as the codecs
// of one lane do.
func sharedLane() func() *codechttp.Codec {
	seen := codechttp.NewSeenCredentials()
	return func() *codechttp.Codec { return codechttp.New(codechttp.Options{Seen: seen}) }
}

// The lane remembers a credential its upstream accepted, so a later
// request that leaves Authorization off and writes the token into its URL
// is refused, on another connection too. A credential the upstream refused
// is not remembered.
func TestTheLaneRemembersWhatTheUpstreamAccepted(t *testing.T) {
	lane := sharedLane()
	authorized := "GET /api HTTP/1.1\r\nAuthorization: Bearer " + bearer + "\r\n\r\n"
	leak := "GET /api/v1/namespaces/default/pods/" + bearer + " HTTP/1.1\r\n\r\n"

	c := lane()
	admit(t, c, authorized)
	answer(t, c, "401 Unauthorized")
	admit(t, lane(), leak)

	c = lane()
	admit(t, c, authorized)
	answer(t, c, "100 Continue") // interim: answers nothing
	answer(t, c, "200 OK")
	mustRefuse(t, lane, leak, "request line")
}

// Answers pair with requests in order, so only the credential the upstream
// accepted is remembered.
func TestAnswersPairWithTheirRequests(t *testing.T) {
	lane := sharedLane()
	refused := "refused-token-0123456789"
	c := lane()
	admit(t, c, "GET /a HTTP/1.1\r\nAuthorization: Bearer "+refused+"\r\n\r\n"+
		"GET /b HTTP/1.1\r\nAuthorization: Bearer "+bearer+"\r\n\r\n")
	answer(t, c, "403 Forbidden")
	answer(t, c, "200 OK")
	admit(t, lane(), "GET /?t="+refused+" HTTP/1.1\r\n\r\n")
	mustRefuse(t, lane, "GET /?t="+bearer+" HTTP/1.1\r\n\r\n", "request line")
}

// A request behind one still awaiting its answer is checked against that
// one's credential: pipelined, it arrives before any answer could teach the
// lane.
func TestAPipelinedRequestIsCheckedAgainstOneInFlight(t *testing.T) {
	r := mustRefuse(t, plainCodec, "GET /a HTTP/1.1\r\nAuthorization: Bearer "+bearer+"\r\n\r\n"+
		"GET /b?t="+bearer+" HTTP/1.1\r\n\r\n", "request line")
	if r.Request != "GET /b" {
		t.Errorf("refused %q, want the second request", r.Request)
	}
}

// A lane that is not strict passes a head it cannot parse through as
// written, and the rest of the connection with it. Those bytes are still
// scanned for the credentials the lane knew.
func TestAnUnframedStreamIsStillScanned(t *testing.T) {
	lane := sharedLane()
	c := lane()
	admit(t, c, "GET / HTTP/1.1\r\nAuthorization: Bearer "+bearer+"\r\n\r\n")
	answer(t, c, "200 OK")
	for _, in := range []string{
		"not a request line " + bearer + "\r\n\r\n",
		"not a request line\r\n\r\nGET /?t=" + bearer + " HTTP/1.1\r\n\r\n",
	} {
		mustRefuse(t, lane, in, "bytes this listener could not frame as requests")
	}
}

// Unparsed, a head's own Authorization cannot be told from a copy of it in
// the URL, so a lane that would pass the head through refuses it instead,
// with no token in its reason.
func TestAnUnparsedHeadWithAnAuthorizationIsRefused(t *testing.T) {
	in := "GET /?k=" + bearer + " HTTP/1.1\r\nAuthorization: Bearer " + bearer + "\r\nno colon on this line\r\n\r\n"
	for _, chunks := range [][]string{{in}, bytewise(in)} {
		c := plainCodec()
		var err error
		for _, chunk := range chunks {
			if _, err = c.Filter(inspect.FromClient, []byte(chunk)); err != nil {
				break
			}
		}
		if err == nil || strings.Contains(err.Error(), bearer) {
			t.Fatalf("Filter in %d chunks = %v; want a refusal that names no token", len(chunks), err)
		}
	}
}

// A server that declines an Upgrade leaves the connection HTTP, so the
// requests after it are framed and checked as before. Once it accepts one,
// the bytes are another protocol and pass as they are.
func TestOnlyAnAcceptedSwitchEndsTheChecks(t *testing.T) {
	upgrade := "GET / HTTP/1.1\r\nUpgrade: h2c\r\nAuthorization: Bearer " + bearer + "\r\n\r\n"
	c := plainCodec()
	admit(t, c, upgrade)
	answer(t, c, "200 OK")
	_, err := c.Filter(inspect.FromClient, []byte("GET /?t="+bearer+" HTTP/1.1\r\n\r\n"))
	var refusal *inspect.PlacementRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Filter after a declined Upgrade = %v; want a PlacementRefusal", err)
	}

	c = plainCodec()
	admit(t, c, upgrade)
	answer(t, c, "101 Switching Protocols")
	admit(t, c, "tunnelled "+bearer)
}

// An unparsed head is checked with its escapes decoded too.
func TestAnUnparsedHeadIsCheckedDecoded(t *testing.T) {
	lane := sharedLane()
	c := lane()
	admit(t, c, "GET / HTTP/1.1\r\nAuthorization: Bearer "+bearer+"\r\n\r\n")
	answer(t, c, "200 OK")
	_, err := lane().Filter(inspect.FromClient,
		[]byte("not a request line /"+strings.ReplaceAll(bearer, "-", "%2D")+"\r\n\r\n"))
	var refusal *inspect.PlacementRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Filter = %v; want a PlacementRefusal", err)
	}
}

// A head over 1 MiB is cut short, so a credential past the cut could not be
// checked: every lane refuses it, a Via-only one too.
func TestEveryLaneRefusesAnOversizedHead(t *testing.T) {
	in := "GET /?t=" + bearer + " HTTP/1.1\r\nX-Pad: " + strings.Repeat("a", 1<<20) +
		"\r\nAuthorization: Bearer " + bearer + "\r\n\r\n"
	if out, err := plainCodec().Filter(inspect.FromClient, []byte(in)); err == nil || len(out) != 0 {
		t.Fatalf("Filter = %d bytes, %v; want a refusal", len(out), err)
	}
}

// A head padded with distinct Authorization lines is refused, not checked
// line by line against each other at the cost of each.
func TestARequestWithTooManyCredentialsIsRefused(t *testing.T) {
	var head strings.Builder
	head.WriteString("GET / HTTP/1.1\r\n")
	for i := range 2000 {
		fmt.Fprintf(&head, "Authorization: Bearer token-%04d-0123456789\r\n", i)
	}
	head.WriteString("\r\n")
	_, err := plainCodec().Filter(inspect.FromClient, []byte(head.String()))
	if err == nil || !strings.Contains(err.Error(), "more credentials than this listener checks") {
		t.Fatalf("Filter = %v; want the credential bound", err)
	}
}
