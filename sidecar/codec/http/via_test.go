package http_test

import (
	"fmt"
	"strings"
	"testing"

	codechttp "github.com/hoophq/hoop/sidecar/codec/http"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// filterChunks runs one client stream through a fresh codec's filter in the
// given pieces and returns everything it let out.
func filterChunks(t *testing.T, chunks ...string) string {
	t.Helper()
	c := codechttp.New(codechttp.Options{})
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

// bytewise splits s into one-byte chunks, the worst split a reader can hand
// the filter.
func bytewise(s string) []string {
	out := make([]string, len(s))
	for i := range s {
		out[i] = s[i : i+1]
	}
	return out
}

// everySplit runs s through a fresh filter once per split point, as two
// reads, and byte by byte, and requires the same output every time.
func everySplit(t *testing.T, s, want string) {
	t.Helper()
	for i := 0; i <= len(s); i++ {
		if got := filterChunks(t, s[:i], s[i:]); got != want {
			t.Fatalf("split at %d:\n got %q\nwant %q", i, got, want)
		}
	}
	if got := filterChunks(t, bytewise(s)...); got != want {
		t.Fatalf("byte by byte:\n got %q\nwant %q", got, want)
	}
}

func via() string { return "Via: 1.1 " + codechttp.Pseudonym() + "\r\n" }

// The mark goes last among the fields, after any Via already present, so a
// recipient combining the lines reads the chain in order. However the head
// is split across reads.
func TestViaIsAddedLastWhateverTheSplit(t *testing.T) {
	head := "GET /x HTTP/1.1\r\nHost: h\r\nVia: 1.0 edge\r\n"
	everySplit(t, head+"\r\n", head+via()+"\r\n")

	// A head written with bare LFs gets a line in the same style.
	lf := "GET /x HTTP/1.1\nHost: h\n"
	everySplit(t, lf+"\n", lf+"Via: 1.1 "+codechttp.Pseudonym()+"\n\n")
}

// Bodies go through byte for byte, framed as the head declares, even when
// they contain what looks like a request head.
func TestViaLeavesBodiesByteExact(t *testing.T) {
	const decoy = "GET /decoy HTTP/1.1\r\nHost: h\r\n\r\n"

	fixed := fmt.Sprintf("POST /x HTTP/1.1\r\nHost: h\r\nContent-Length: %d\r\n", len(decoy))
	everySplit(t, fixed+"\r\n"+decoy, fixed+via()+"\r\n"+decoy)

	chunked := "POST /x HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n"
	body := "5;name=value\r\nhello\r\n" +
		fmt.Sprintf("%x\r\n", len(decoy)) + decoy + "\r\n" +
		"0\r\nX-Trailer: t\r\n\r\n"
	everySplit(t, chunked+"\r\n"+body, chunked+via()+"\r\n"+body)
}

// Pipelined requests in one read each get exactly one mark, and the body of
// one does not swallow the next head.
func TestViaMarksEachPipelinedRequest(t *testing.T) {
	a := "GET /a HTTP/1.1\r\nHost: h\r\n"
	b := "POST /b HTTP/1.1\r\nHost: h\r\nContent-Length: 4\r\n"
	c := "POST /c HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n"
	in := a + "\r\n" + b + "\r\nbody" + c + "\r\n3\r\nabc\r\n0\r\n\r\n"
	want := a + via() + "\r\n" + b + via() + "\r\nbody" + c + via() + "\r\n3\r\nabc\r\n0\r\n\r\n"
	everySplit(t, in, want)
}

// A request that already carries this process's mark came back around: the
// sidecar's own upstream connection was routed into its listener. It is
// refused, with a message naming the fix, and the connection stays refused.
func TestViaRefusesItsOwnMark(t *testing.T) {
	c := codechttp.New(codechttp.Options{})
	entry := "1.1 " + strings.ToUpper(codechttp.Pseudonym()) + " (lap one)"
	req := "GET /x HTTP/1.1\r\nHost: h\r\nVia: 1.0 edge, " + entry + "\r\n\r\n"
	out, err := c.Filter(inspect.FromClient, []byte(req))
	if err == nil {
		t.Fatalf("a request carrying this sidecar's own Via was forwarded as %q", out)
	}
	if !strings.Contains(err.Error(), "request loop") || !strings.Contains(err.Error(), entry) {
		t.Errorf("the refusal does not say what happened: %v", err)
	}
	if _, err := c.Filter(inspect.FromClient, []byte(get("h", "/next"))); err == nil {
		t.Error("the connection accepted more requests after a loop was detected")
	}
}

// Another proxy's pseudonym is not ours, not even another sidecar's, and
// neither is our name inside a comment.
func TestViaAcceptsOtherProxiesMarks(t *testing.T) {
	for _, v := range []string{
		"1.1 hoop-0000000000000000",
		"1.1 edge (relayed for 1.1 " + codechttp.Pseudonym() + ", honestly)",
		"HTTP/1.1 " + codechttp.Pseudonym() + ".example.com",
	} {
		head := "GET /x HTTP/1.1\r\nHost: h\r\nVia: " + v + "\r\n"
		if got := filterChunks(t, head+"\r\n"); got != head+via()+"\r\n" {
			t.Errorf("Via %q: got %q", v, got)
		}
	}
}

// Once a request asks to leave HTTP/1 the rest of the connection is another
// protocol, and a line inserted into it would corrupt it.
func TestViaStopsAfterAProtocolSwitch(t *testing.T) {
	const later = "GET /later HTTP/1.1\r\nHost: h\r\n\r\n"
	for _, head := range []string{
		"GET /ws HTTP/1.1\r\nHost: h\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n",
		"CONNECT db:5432 HTTP/1.1\r\nHost: db:5432\r\n",
	} {
		frames := "\x81\x05hello" + later
		everySplit(t, head+"\r\n"+frames, head+via()+"\r\n"+frames)
	}
}

// What the filter cannot frame as HTTP/1 it leaves alone: the codec reports
// such a stream itself, and a line added to it would only add damage. The
// server stream is never rewritten.
func TestViaLeavesWhatItCannotFrameAlone(t *testing.T) {
	for _, in := range []string{
		"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n" + get("h", "/after-preface"),
		"not a request line\r\n\r\n" + get("h", "/after-garbage"),
	} {
		if got := filterChunks(t, in); got != in {
			t.Errorf("rewrote a stream it cannot frame:\n got %q\nwant %q", got, in)
		}
	}

	c := codechttp.New(codechttp.Options{})
	resp := "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"
	if got, err := c.Filter(inspect.FromServer, []byte(resp)); err != nil || string(got) != resp {
		t.Errorf("server stream: %q, %v; want it unchanged", got, err)
	}
}
