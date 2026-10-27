package upstreamcred

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// token is long enough for MinTokenBytes and distinctive enough that a
// substring search for it finds only a leak.
const token = "upstream-secret-7f3a9c2e41d8b605"

func write(t *testing.T, path, content string, mode os.FileMode, mod time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile keeps the mode of a file that exists, and the umask trims a
	// new one.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func tokenFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api-token")
	write(t, path, content, 0o600, time.Now().Add(-time.Hour))
	return path
}

func TestSpecCheck(t *testing.T) {
	file := "/run/secrets/api-token"
	for _, tc := range []struct {
		name string
		spec Spec
		want string // a substring of the joined problems; "" for none
	}{
		{"authorization", Spec{Header: "Authorization", Scheme: "Bearer", File: file}, ""},
		{"api key, no scheme", Spec{Header: "X-API-Key", File: file}, ""},
		{"cookie", Spec{Header: "Cookie", File: file}, ""},
		{"no header", Spec{File: file}, "header is required"},
		{"not a header name", Spec{Header: "X API Key", File: file}, "not a header name"},
		{"framing", Spec{Header: "Content-Length", File: file}, "frames or routes"},
		{"chunking", Spec{Header: "transfer-encoding", File: file}, "frames or routes"},
		{"routing", Spec{Header: "Host", File: file}, "frames or routes"},
		{"sidecar's own", Spec{Header: "Via", File: file}, "frames or routes"},
		{"proxy family", Spec{Header: "Proxy-Authorization", File: file}, "frames or routes"},
		{"forwarded family", Spec{Header: "X-Forwarded-For", File: file}, "frames or routes"},
		{"scheme with a space", Spec{Header: "Authorization", Scheme: "Bearer token", File: file}, "not an auth scheme"},
		{"no file", Spec{Header: "Authorization"}, "file is required"},
	} {
		got := strings.Join(tc.spec.Check(), "; ")
		if tc.want == "" && got != "" {
			t.Errorf("%s: Check = %q, want none", tc.name, got)
		}
		if tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%s: Check = %q, want one containing %q", tc.name, got, tc.want)
		}
	}
}

func TestOpenSendsTheTokenUnderItsScheme(t *testing.T) {
	path := tokenFile(t, "\ufeff  "+token+"\n")
	for _, tc := range []struct {
		scheme, want string
	}{
		{"Bearer", "Bearer " + token},
		{"", token},
	} {
		c, err := Open(Spec{Header: "Authorization", Scheme: tc.scheme, File: path}, nil)
		if err != nil {
			t.Fatal(err)
		}
		value, tok := c.Current()
		if value != tc.want || tok != token {
			t.Errorf("scheme %q: Current = %q, %q; want %q, %q", tc.scheme, value, tok, tc.want, token)
		}
		if c.Header() != "Authorization" {
			t.Errorf("Header = %q, want Authorization as configured", c.Header())
		}
	}
}

// Every refusal names the file, and none carries what the file holds: the
// error reaches the startup log and -validate's report.
func TestOpenRefusesAFileItCannotSend(t *testing.T) {
	dir := t.TempDir()
	loose := filepath.Join(dir, "loose")
	write(t, loose, token, 0o644, time.Now())
	link := filepath.Join(dir, "link")
	if err := os.Symlink(loose, link); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		path, want string
	}{
		"missing":                 {filepath.Join(dir, "missing"), "no such file"},
		"directory":               {dir, "is a directory"},
		"readable by others":      {loose, "0644"},
		"link to a readable file": {link, "0644"},
		"empty":                   {tokenFile(t, " \n"), "holds no token"},
		"CRLF inside":             {tokenFile(t, token+"\r\nX-Injected: 1"), "control"},
		"LF inside":               {tokenFile(t, token+"\nsecond-line-of-it"), "control"},
		"NUL inside":              {tokenFile(t, token+"\x00"+token), "control"},
		"tab inside":              {tokenFile(t, token+"\t"+token), "control"},
		"non-ASCII":               {tokenFile(t, token+"é"), "non-ASCII"},
		"short":                   {tokenFile(t, token[:MinTokenBytes-1]), "shorter than"},
		"oversize":                {tokenFile(t, strings.Repeat("a", maxTokenBytes+1)), "more than"},
	} {
		_, err := Open(Spec{Header: "Authorization", Scheme: "Bearer", File: tc.path}, nil)
		if err == nil {
			t.Errorf("%s: Open accepted it", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.path) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %q, want one naming %s and saying %q", name, err, tc.path, tc.want)
		}
		if strings.Contains(err.Error(), token[:MinTokenBytes-1]) || strings.Contains(err.Error(), "X-Injected") {
			t.Errorf("%s: error carries the file's content: %q", name, err)
		}
	}
}

func TestOpenRefusesABadSpecBeforeReadingAnything(t *testing.T) {
	path := tokenFile(t, token)
	if _, err := Open(Spec{Header: "Content-Length", File: path}, nil); err == nil || !strings.Contains(err.Error(), "frames or routes") {
		t.Fatalf("Open error = %v, want the header refused", err)
	}
}

// A rotation lands on the next Poll, and a read that fails keeps the token
// in force, as the caller token file does: a rewrite caught mid-write must
// not cut the lane off from its upstream.
func TestPollRotatesAndKeepsTheTokenOnABadRead(t *testing.T) {
	path := tokenFile(t, token)
	base := time.Now().Add(-time.Hour)
	var logs bytes.Buffer
	c, err := Open(Spec{Header: "Authorization", Scheme: "Bearer", File: path}, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}

	rotated := "rotated-secret-0b1c2d3e4f5a6978"
	write(t, path, rotated+"\n", 0o600, base.Add(time.Second))
	c.Poll()
	if _, tok := c.Current(); tok != rotated {
		t.Fatalf("after a rotation the token is %q, want %q", tok, rotated)
	}

	for i, br := range []func(){
		func() { write(t, path, "", 0o600, base.Add(2*time.Second)) },
		func() { write(t, path, "bad\r\nX-Injected: 1", 0o600, base.Add(3*time.Second)) },
		func() { write(t, path, rotated, 0o644, base.Add(4*time.Second)) },
		func() {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		},
	} {
		br()
		c.Poll()
		if _, tok := c.Current(); tok != rotated {
			t.Fatalf("breakage %d replaced the token in force with %q", i, tok)
		}
	}
	if n := strings.Count(logs.String(), "level=WARN"); n != 1 {
		t.Errorf("warned %d times across one broken stretch, want once", n)
	}
	for _, s := range []string{token, rotated, "X-Injected"} {
		if strings.Contains(logs.String(), s) {
			t.Errorf("the log carries %q:\n%s", s, logs.String())
		}
	}
}

// A credential formatted any way a log line, an error or a JSON dump might
// format it shows no token.
func TestACredentialRendersRedacted(t *testing.T) {
	c, err := Open(Spec{Header: "Authorization", Scheme: "Bearer", File: tokenFile(t, token)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	holder := struct{ Cred *Credential }{c}
	j, err := json.Marshal(holder)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("lane", "credential", c)
	slog.New(slog.NewTextHandler(&logs, nil)).Info("lane", "credential", c)
	for _, s := range []string{
		fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c), fmt.Sprintf("%s", c),
		fmt.Sprintf("%v", holder), fmt.Sprintf("%+v", holder), fmt.Sprintf("%#v", holder),
		string(j), logs.String(),
	} {
		if strings.Contains(s, token) {
			t.Errorf("rendered %q", s)
		}
	}
}
