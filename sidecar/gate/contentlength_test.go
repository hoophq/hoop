package gate

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// resp builds a response whose Content-Length matches its body.
func resp(headers, body string) []byte {
	return []byte("HTTP/1.1 200 OK\r\n" +
		"Content-Type: application/json\r\n" +
		headers +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" +
		body)
}

// declaredLength reads the Content-Length a client would act on.
func declaredLength(t *testing.T, payload []byte) int {
	t.Helper()
	head, _, ok := bytes.Cut(payload, []byte("\r\n\r\n"))
	if !ok {
		t.Fatalf("no header block in %q", payload)
	}
	for _, line := range strings.Split(string(head), "\r\n") {
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "content-length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				t.Fatalf("unparseable Content-Length %q", value)
			}
			return n
		}
	}
	t.Fatal("no Content-Length header")
	return 0
}

// bodyOf returns the bytes after the header block.
func bodyOf(t *testing.T, payload []byte) []byte {
	t.Helper()
	_, body, ok := bytes.Cut(payload, []byte("\r\n\r\n"))
	if !ok {
		t.Fatalf("no header block")
	}
	return body
}

// A client reads exactly Content-Length bytes, so a stale header truncates
// the document mid-token. That is the bug this file guards against.
func TestRetagKeepsHeaderConsistentWithBody(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		delta int
	}{
		{"body grew", `{"email":"[REDACTED:email]"}`, +1},
		{"body shrank", `{"e":"***"}`, -4},
		{"width changes 3 to 4 digits", strings.Repeat("x", 1002), +5},
		{"width changes 4 to 3 digits", strings.Repeat("x", 998), -5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Build a response as it looked BEFORE masking, then splice in a
			// masked body of a different length, exactly as the masker does.
			original := resp("", strings.Repeat("y", len(tc.body)-tc.delta))
			masked := bytes.Replace(original,
				bodyOf(t, original), []byte(tc.body), 1)

			out, ok := retagContentLength(masked, tc.delta)
			if !ok {
				t.Fatalf("refused to retag a complete, well-formed response")
			}

			if got, want := declaredLength(t, out), len(tc.body); got != want {
				t.Errorf("Content-Length = %d, body is %d bytes: a client would %s",
					got, want,
					map[bool]string{true: "truncate", false: "hang waiting for more"}[got < want])
			}
			if got := string(bodyOf(t, out)); got != tc.body {
				t.Errorf("body altered: %q", got)
			}
		})
	}
}

func TestRetagNoopOnZeroDelta(t *testing.T) {
	in := resp("", `{"a":"b"}`)
	out, ok := retagContentLength(in, 0)
	if !ok {
		t.Error("a length-preserving mask left the declared length accurate; want ok")
	}
	if &out[0] != &in[0] {
		t.Error("zero delta should return the input unchanged, not a copy")
	}
}

// Each refusal below is deliberate: a WRONG Content-Length desynchronizes a
// keep-alive connection for every request that follows, which is worse than
// leaving a truncated body for this one.
func TestRetagRefusesWhenItCannotBeSure(t *testing.T) {
	body := `{"email":"[REDACTED:email]"}`

	for _, tc := range []struct {
		name    string
		payload []byte
		delta   int
	}{
		{
			// Header block not in this buffer: the header already went out
			// and cannot be corrected now.
			"body-only chunk",
			[]byte(body),
			+1,
		},
		{
			// Chunked framing carries its own lengths.
			"chunked response",
			[]byte("HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n1c\r\n" + body + "\r\n0\r\n\r\n"),
			+1,
		},
		{
			// Two Content-Lengths is a smuggling shape; do not pick one.
			"duplicate content-length",
			[]byte("HTTP/1.1 200 OK\r\nContent-Length: 27\r\nContent-Length: 28\r\n\r\n" + body),
			+1,
		},
		{
			"unparseable content-length",
			[]byte("HTTP/1.1 200 OK\r\nContent-Length: banana\r\n\r\n" + body),
			+1,
		},
		{
			// Not a response: could be a body shaped like headers.
			"no status line",
			[]byte("Content-Length: 27\r\n\r\n" + body),
			+1,
		},
		{
			// The declared length does not describe the bytes present, so the
			// buffer holds only part of the entity and delta is not the whole
			// story.
			"partial body",
			[]byte("HTTP/1.1 200 OK\r\nContent-Length: 5000\r\n\r\n" + body),
			+1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, ok := retagContentLength(tc.payload, tc.delta)
			if ok {
				t.Error("reported success on a payload it cannot correct; the caller " +
					"would ship a masked body behind a stale Content-Length")
			}
			if !bytes.Equal(out, tc.payload) {
				t.Errorf("payload was modified when it should have been left alone:\n got %q\nwant %q",
					out, tc.payload)
			}
		})
	}
}

// Header names are case-insensitive per RFC 9110; an upstream writing
// "content-length" must still get a correct number.
func TestRetagMatchesHeaderCaseInsensitively(t *testing.T) {
	body := `{"email":"[REDACTED:email]"}`
	for _, name := range []string{"Content-Length", "content-length", "CONTENT-LENGTH", "Content-length"} {
		payload := []byte("HTTP/1.1 200 OK\r\n" + name + ": " + strconv.Itoa(len(body)-1) + "\r\n\r\n" + body)
		out, ok := retagContentLength(payload, +1)
		if !ok {
			t.Fatalf("refused header name %q", name)
		}
		if got := declaredLength(t, out); got != len(body) {
			t.Errorf("%s: Content-Length = %d, want %d", name, got, len(body))
		}
	}
}

// Other headers, and the body itself, must survive untouched.
func TestRetagPreservesEverythingElse(t *testing.T) {
	body := `{"email":"[REDACTED:email]"}`
	original := resp("Set-Cookie: a=b\r\nX-Trace: 12345\r\n", strings.Repeat("y", len(body)-1))
	masked := bytes.Replace(original, bodyOf(t, original), []byte(body), 1)

	out, _ := retagContentLength(masked, +1)

	for _, want := range []string{"Set-Cookie: a=b", "X-Trace: 12345", "Content-Type: application/json", "HTTP/1.1 200 OK"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("lost %q from:\n%s", want, out)
		}
	}
	if got := string(bodyOf(t, out)); got != body {
		t.Errorf("body = %q, want %q", got, body)
	}
}

// A masked body that becomes empty must declare zero, not a negative number.
func TestRetagNeverEmitsNegative(t *testing.T) {
	payload := []byte("HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\n")
	// delta -5 with an empty body present: declared 5, body 0, so 0 == 5-5.
	out, _ := retagContentLength(payload, -5)
	if got := declaredLength(t, out); got != 0 {
		t.Errorf("Content-Length = %d, want 0", got)
	}
	if !bytes.HasSuffix(out, []byte("\r\n\r\n")) {
		t.Errorf("body should still be empty: %q", out)
	}
}

// The end-to-end property: after retagging, a client reads exactly what the
// masker produced.
func TestClientReadsTheWholeMaskedBody(t *testing.T) {
	for _, masked := range []string{
		`{"cpf":"[REDACTED:BR_CPF]"}`,
		`{"iban":"******************5432"}`,
		`{"a":"[REDACTED:email]","b":"[REDACTED:BR_CPF]","c":"***"}`,
	} {
		original := resp("", strings.Repeat("y", 20))
		payload := bytes.Replace(original, bodyOf(t, original), []byte(masked), 1)
		delta := len(masked) - 20

		out, _ := retagContentLength(payload, delta)

		// Simulate the client: read exactly Content-Length bytes of body.
		n := declaredLength(t, out)
		body := bodyOf(t, out)
		if n > len(body) {
			t.Fatalf("declared %d but only %d bytes present", n, len(body))
		}
		if got := string(body[:n]); got != masked {
			t.Errorf("client read %q, masker produced %q", got, masked)
		}
	}
}

func ExampleretagContentLength() {
	body := `{"email":"[REDACTED:email]"}` // 16-byte value replaced a 15-byte one
	payload := []byte("HTTP/1.1 200 OK\r\nContent-Length: " +
		strconv.Itoa(len(body)-1) + "\r\n\r\n" + body)

	out, _ := retagContentLength(payload, +1)

	head, _, _ := bytes.Cut(out, []byte("\r\n\r\n"))
	fmt.Println(strings.ReplaceAll(string(head), "\r\n", " | "))
	// Output: HTTP/1.1 200 OK | Content-Length: 28
}
