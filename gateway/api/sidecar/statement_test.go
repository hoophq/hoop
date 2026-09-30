package apisidecar

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func notice(n int) string {
	return fmt.Sprintf(`[binary statement, %d bytes: a byte that is not printable shows as \xNN, a backslash as \\]`, n)
}

// parseDisplayStatement is displayStatement's inverse. It exists only to prove
// the display loses nothing: a reviewer reading it reads the raw bytes.
func parseDisplayStatement(d string) ([]byte, error) {
	if !strings.HasPrefix(d, binaryNoticePrefix) {
		return []byte(d), nil
	}
	nl := strings.IndexByte(d, '\n')
	if nl < 0 {
		return nil, errors.New("notice has no line end")
	}
	head, body := d[:nl], d[nl+1:]
	n, err := strconv.Atoi(strings.TrimPrefix(strings.SplitN(head, " bytes", 2)[0], binaryNoticePrefix))
	if err != nil {
		return nil, fmt.Errorf("notice byte count: %w", err)
	}
	if head != notice(n) {
		return nil, fmt.Errorf("notice %q is not the notice for %d bytes", head, n)
	}
	var out []byte
	for i := 0; i < len(body); {
		if body[i] != '\\' {
			out = append(out, body[i])
			i++
			continue
		}
		switch {
		case i+1 < len(body) && body[i+1] == '\\':
			out = append(out, '\\')
			i += 2
		case i+3 < len(body) && body[i+1] == 'x':
			v, err := strconv.ParseUint(body[i+2:i+4], 16, 8)
			if err != nil {
				return nil, err
			}
			out = append(out, byte(v))
			i += 4
		default:
			return nil, fmt.Errorf("unknown escape at %d", i)
		}
	}
	if len(out) != n {
		return nil, fmt.Errorf("decoded %d bytes, the notice says %d", len(out), n)
	}
	return out, nil
}

// checkDisplay asserts what storing and reading the display depends on.
func checkDisplay(t *testing.T, raw []byte) {
	t.Helper()
	d := displayStatement(raw)
	if !utf8.ValidString(d) {
		t.Fatalf("display of %q is not valid UTF-8", raw)
	}
	// The expression both blob writers use: models/session.go (upsertSessionTx)
	// and models/reviews.go (createReviewTx).
	enc := fmt.Sprintf("[%q]", d)
	if !json.Valid([]byte(enc)) {
		t.Fatalf("the stored blob of %q is not JSON: %s", raw, enc)
	}
	if strings.Contains(enc, `\u0000`) {
		t.Fatalf("the stored blob of %q carries NUL, which jsonb refuses", raw)
	}
	var back []string
	if err := json.Unmarshal([]byte(enc), &back); err != nil || len(back) != 1 || back[0] != d {
		t.Fatalf("the blob of %q does not read back: %v %q", raw, err, back)
	}
	got, err := parseDisplayStatement(d)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("display of %q does not decode back: %q %v", raw, got, err)
	}
	if isPrintableText(raw) != (d == string(raw)) {
		t.Fatalf("display of %q: text mode must return the raw text exactly", raw)
	}
}

func TestDisplayStatementKeepsPrintableTextAsIs(t *testing.T) {
	for _, s := range []string{
		"",
		"SELECT 1;",
		"SELECT 1;\r\n\tFROM t",
		`E'\x41'`,
		"café 日本",
		"\uFFFD",
		"cafe\u0301",
		"POST /api/v1/namespaces/default/configmaps\n\n{\"kind\":\"ConfigMap\"}",
	} {
		t.Run(s, func(t *testing.T) {
			assert.Equal(t, s, displayStatement([]byte(s)))
			checkDisplay(t, []byte(s))
		})
	}
}

func TestDisplayStatementEscapesBytesThatAreNotPrintable(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"k8s\x00\n\x04Pod", "k8s" + `\x00` + "\n" + `\x04Pod`},
		{"a\tb\x00", `a\x09b\x00`},
		{"a\\b\x00", `a\\b\x00`},
		{"caf\xe9", `caf\xe9`},
		{"a\u202eb", `a\xe2\x80\xaeb`},
		{"a\u00a0b", `a\xc2\xa0b`},
		{"\x7f", `\x7f`},
		{"a\U000E0001b", `a\xf3\xa0\x80\x81b`},
		{"\xed\xa0\x80", `\xed\xa0\x80`},
		{"\xef\xbf\xbd\xff", "\uFFFD" + `\xff`},
		{"a\bb\fc", `a\x08b\x0cc`},
		{"a\r\nb\x01", `a\x0d` + "\n" + `b\x01`},
		// Printable to strconv.IsPrint, blank to a reviewer.
		{"a\u3164b", `a\xe3\x85\xa4b`},
		{"a\ufe0fb", `a\xef\xb8\x8fb`},
		{"a\u2800b", `a\xe2\xa0\x80b`},
		{"a\u034fb", `a\xcd\x8fb`},
		// A lone \r can hide what came before it.
		{"a\rb", `a\x0db`},
		// In binary form a combining mark would draw over the escape.
		{"\x00\u0338", `\x00\xcc\xb8`},
	} {
		t.Run(strconv.Quote(tc.raw), func(t *testing.T) {
			assert.Equal(t, notice(len(tc.raw))+"\n"+tc.want, displayStatement([]byte(tc.raw)))
			checkDisplay(t, []byte(tc.raw))
		})
	}
}

// A text statement that looks like the notice is shown in binary form, so the
// notice a reviewer reads always came from displayStatement.
func TestDisplayStatementNeverShowsTextAsTheNotice(t *testing.T) {
	raw := "[binary statement, 3 bytes: x]\nabc"
	d := displayStatement([]byte(raw))
	assert.Equal(t, notice(len(raw))+"\n"+raw, d)
	checkDisplay(t, []byte(raw))

	for _, lead := range []string{" ", "\t", "\r\n", "\n"} {
		spoof := lead + binaryNoticePrefix + "3 bytes: x]\nabc"
		assert.True(t, strings.HasPrefix(displayStatement([]byte(spoof)), notice(len(spoof))+"\n"),
			"leading %q must not hide the notice guard", lead)
		checkDisplay(t, []byte(spoof))
	}

	binary := []byte("k8s\x00")
	assert.NotEqual(t, displayStatement(binary), displayStatement([]byte(displayStatement(binary))))
}

func TestDisplayStatementSurvivesTheBlobEncoding(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []byte("\\x0a\n\t\r\"[b")
	for range 200000 {
		n := rng.Intn(41)
		raw := make([]byte, 0, n+4)
		if rng.Intn(4) == 0 {
			raw = utf8.AppendRune(raw, rune(rng.Intn(0x110000)))
		}
		for range n {
			if rng.Intn(2) == 0 {
				raw = append(raw, byte(rng.Intn(256)))
			} else {
				raw = append(raw, alphabet[rng.Intn(len(alphabet))])
			}
		}
		checkDisplay(t, raw)
	}
}

func FuzzDisplayStatement(f *testing.F) {
	for _, seed := range []string{"k8s\x00", `\x00`, binaryNoticePrefix, "\xff", ""} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) { checkDisplay(t, raw) })
}

// The cap is on the raw bytes. This pins the bound the maxStatementBytes
// comment states for the stored display.
func TestDisplayStatementIsBoundedByTheCap(t *testing.T) {
	d := displayStatement(make([]byte, maxStatementBytes))
	require.Equal(t, len(notice(maxStatementBytes))+1+4*maxStatementBytes, len(d))
}
