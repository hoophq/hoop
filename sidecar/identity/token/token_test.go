package token

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func tokenFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tokens")
	writeFile(t, path, content)
	return path
}

func TestParseReadsOneTokenPerLine(t *testing.T) {
	s, err := parse([]byte("\n  alpha  \r\n\n\tbeta\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}
	for _, tok := range []string{"alpha", "beta"} {
		if !s.Match(tok) {
			t.Errorf("Match(%q) = false", tok)
		}
	}
	for _, tok := range []string{"", "alph", "alphaa", " alpha", "gamma", "alpha, beta"} {
		if s.Match(tok) {
			t.Errorf("Match(%q) = true", tok)
		}
	}
}

// An editor's byte order mark is not part of the first token.
func TestParseIgnoresAByteOrderMark(t *testing.T) {
	s, err := parse([]byte("\ufeffalpha\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Match("alpha") {
		t.Error("the first token kept the byte order mark")
	}
}

func TestParseRefusesWhatAHeaderCannotCarryAlone(t *testing.T) {
	for name, raw := range map[string]string{
		"inner space": "ok\nsecret value\n",
		"comma":       "ok\nsecret,value\n",
		"tab":         "ok\nsecret\tvalue\n",
		"control":     "ok\nsecret\x01value\n",
		"delete":      "ok\nsecret\x7fvalue\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parse([]byte(raw))
			if err == nil {
				t.Fatal("Parse accepted it")
			}
			if !strings.Contains(err.Error(), "line 2") {
				t.Errorf("error %q does not name the line", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("error %q carries the token", err)
			}
		})
	}
}

func TestParseRefusesAFileWithNoToken(t *testing.T) {
	for _, raw := range []string{"", "\n", "  \n\t\r\n"} {
		if _, err := parse([]byte(raw)); err == nil {
			t.Errorf("parse(%q) accepted a set with no token", raw)
		}
	}
}

func TestLoadErrorsNameThePathAndNeverTheContent(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	empty := tokenFile(t, "\n")
	bad := tokenFile(t, "secret value\n")
	big := tokenFile(t, strings.Repeat("secret\n", maxFileBytes/7+1))
	for name, path := range map[string]string{"missing": missing, "empty": empty, "bad": bad, "too big": big} {
		t.Run(name, func(t *testing.T) {
			_, err := load(path)
			if err == nil {
				t.Fatal("Load accepted it")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error %q does not name %s", err, path)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("error %q carries the content", err)
			}
		})
	}
}

func TestResolverAdmitsOnlyAListedToken(t *testing.T) {
	s, err := parse([]byte("good\nalso-good\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewResolver(" claude-agents ", s)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, credential string
		want             error
	}{
		{"empty", "", ErrNoToken},
		{"whitespace only", " \t ", ErrNoToken},
		{"wrong", "bad-token", ErrInvalidToken},
		{"prefix", "goo", ErrInvalidToken},
		// The codec joins a repeated header with ", ". Two copies of a
		// good token are still not a token.
		{"repeated header", "good, good", ErrInvalidToken},
		{"two tokens joined", "good, also-good", ErrInvalidToken},
	} {
		t.Run(c.name, func(t *testing.T) {
			id, err := r.Resolve(context.Background(), c.credential)
			if !errors.Is(err, c.want) {
				t.Fatalf("Resolve(%q) error = %v, want %v", c.credential, err, c.want)
			}
			if !id.IsAnonymous() {
				t.Errorf("a refused credential named %q", id.Principal())
			}
			if v := strings.TrimSpace(c.credential); v != "" && strings.Contains(err.Error(), v) {
				t.Errorf("error %q carries the credential", err)
			}
		})
	}
	for _, credential := range []string{"good", "  also-good \t"} {
		id, err := r.Resolve(context.Background(), credential)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", credential, err)
		}
		if id.Subject != "claude-agents" {
			t.Errorf("Subject = %q, want the configured name, trimmed", id.Subject)
		}
	}
}

func TestNewResolverRefusesABlankCallerName(t *testing.T) {
	s, _ := parse([]byte("good"))
	if _, err := NewResolver("  ", s); err == nil {
		t.Error("NewResolver accepted a blank caller name")
	}
}

// rewrite replaces the file's content and moves its mtime, so a Poll sees
// the change however coarse the filesystem's clock is.
func rewrite(t *testing.T, path, content string, mod time.Time) {
	t.Helper()
	writeFile(t, path, content)
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func TestFileOpenRefusesAFileItCannotUse(t *testing.T) {
	for name, path := range map[string]string{
		"missing": filepath.Join(t.TempDir(), "missing"),
		"empty":   tokenFile(t, ""),
	} {
		if _, err := Open(path, nil); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: Open error = %v, want one naming %s", name, err, path)
		}
	}
}

func TestFilePollRotatesTokens(t *testing.T) {
	path := tokenFile(t, "old\n")
	base := time.Now().Add(-time.Hour)
	rewrite(t, path, "old\n", base)
	f, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Overlap: the new token runs beside the old one.
	rewrite(t, path, "old\nnew\n", base.Add(time.Second))
	f.Poll()
	if !f.Match("old") || !f.Match("new") {
		t.Fatalf("during the overlap old=%v new=%v, want both", f.Match("old"), f.Match("new"))
	}

	// Then the old line goes.
	rewrite(t, path, "new\n", base.Add(2*time.Second))
	f.Poll()
	if f.Match("old") || !f.Match("new") {
		t.Fatalf("after rotation old=%v new=%v, want only new", f.Match("old"), f.Match("new"))
	}
}

func TestFilePollReadsNothingWhenTheStatDidNotMove(t *testing.T) {
	path := tokenFile(t, "aaaa\n")
	mod := time.Now().Add(-time.Hour)
	rewrite(t, path, "aaaa\n", mod)
	f, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Same size, same mtime: the stat cannot see it, so Poll keeps aaaa.
	rewrite(t, path, "bbbb\n", mod)
	f.Poll()
	if !f.Match("aaaa") || f.Match("bbbb") {
		t.Fatal("Poll re-read a file whose stat did not move")
	}
	// Reload is SIGHUP's forced read: it sees it.
	f.Reload()
	if f.Match("aaaa") || !f.Match("bbbb") {
		t.Fatal("Reload did not re-read the file")
	}
}

func TestFileBadReloadKeepsTheTokensInForce(t *testing.T) {
	path := tokenFile(t, "good\n")
	base := time.Now().Add(-time.Hour)
	rewrite(t, path, "good\n", base)
	var logs bytes.Buffer
	f, err := Open(path, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}

	breakages := []func(){
		func() { rewrite(t, path, "", base.Add(time.Second)) },                // emptied mid-write
		func() { rewrite(t, path, "not a token\n", base.Add(2*time.Second)) }, // malformed
		func() {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		},
	}
	for _, br := range breakages {
		br()
		f.Poll()
		f.Poll() // a retry while still broken
		if !f.Match("good") {
			t.Fatal("a bad reload dropped the token in force")
		}
	}
	if n := strings.Count(logs.String(), "level=WARN"); n != 1 {
		t.Errorf("warned %d times across one broken stretch, want once", n)
	}

	// Fixed, even with a stat the file once had: the failed read keeps the
	// next Poll reading.
	rewrite(t, path, "fresh\n", base)
	f.Poll()
	if f.Match("good") || !f.Match("fresh") {
		t.Fatal("the fixed file was not read")
	}

	// Broken again: a new stretch warns again.
	rewrite(t, path, "", base.Add(3*time.Second))
	f.Poll()
	if n := strings.Count(logs.String(), "level=WARN"); n != 2 {
		t.Errorf("warned %d times across two broken stretches, want twice", n)
	}
	for _, tok := range []string{"good", "fresh", "not a token"} {
		if strings.Contains(logs.String(), tok) {
			t.Errorf("the log carries %q", tok)
		}
	}
}
