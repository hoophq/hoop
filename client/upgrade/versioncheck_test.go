package upgrade

import (
	"bytes"
	"strings"
	"testing"
)

// withLocalVersion swaps the local-version provider for the duration of the
// test and restores the original on cleanup.
func withLocalVersion(t *testing.T, ver string) {
	t.Helper()
	prev := localVersionFn
	localVersionFn = func() string { return ver }
	t.Cleanup(func() {
		localVersionFn = prev
		resetVersionWarnStateForTests()
	})
}

func TestParseServerHeader(t *testing.T) {
	cases := map[string]struct {
		want string
		ok   bool
	}{
		"hoopgateway/1.72.0":                       {"1.72.0", true},
		"hoopgateway/1.72.0-rc1+sha":               {"1.72.0-rc1+sha", true},
		"  hoopgateway/1.72.0 ":                    {"1.72.0", true},
		"hoopgateway/":                             {"", false},
		"nginx/1.25":                               {"", false},
		"":                                         {"", false},
		"AmazonS3":                                 {"", false},
		"hoopgateway/  ":                           {"", false},
	}
	for in, exp := range cases {
		got, ok := parseServerHeader(in)
		if ok != exp.ok || got != exp.want {
			t.Errorf("parseServerHeader(%q): want (%q,%v) got (%q,%v)", in, exp.want, exp.ok, got, ok)
		}
	}
}

func TestWarnOnVersionMismatch(t *testing.T) {
	withLocalVersion(t, "1.72.0")
	var buf bytes.Buffer
	warnOnceFromServerHeaderTo(&buf, "hoopgateway/1.73.0")
	out := buf.String()
	if !strings.Contains(out, "1.72.0") || !strings.Contains(out, "1.73.0") {
		t.Fatalf("expected warning to mention both versions, got %q", out)
	}
	if !strings.Contains(out, "hoop versions sync") {
		t.Fatalf("expected warning to suggest `hoop versions sync`, got %q", out)
	}
}

func TestNoWarnOnVersionMatch(t *testing.T) {
	withLocalVersion(t, "1.72.0")
	var buf bytes.Buffer
	warnOnceFromServerHeaderTo(&buf, "hoopgateway/1.72.0")
	if buf.Len() != 0 {
		t.Fatalf("expected no warning, got %q", buf.String())
	}
}

func TestNoWarnOnUnknownLocalVersion(t *testing.T) {
	for _, local := range []string{"", "unknown"} {
		withLocalVersion(t, local)
		var buf bytes.Buffer
		warnOnceFromServerHeaderTo(&buf, "hoopgateway/1.73.0")
		if buf.Len() != 0 {
			t.Fatalf("local=%q: expected no warning, got %q", local, buf.String())
		}
	}
}

func TestNoWarnWhenHeaderMissingOrForeign(t *testing.T) {
	withLocalVersion(t, "1.72.0")
	for _, header := range []string{"", "nginx/1.25", "AmazonS3", "hoopgateway/"} {
		var buf bytes.Buffer
		warnOnceFromServerHeaderTo(&buf, header)
		if buf.Len() != 0 {
			t.Fatalf("header=%q: expected no warning, got %q", header, buf.String())
		}
		resetVersionWarnStateForTests()
	}
}

func TestWarnIsOneShot(t *testing.T) {
	withLocalVersion(t, "1.72.0")
	var buf bytes.Buffer
	for range 5 {
		warnOnceFromServerHeaderTo(&buf, "hoopgateway/1.73.0")
	}
	if got := strings.Count(buf.String(), "differs from gateway"); got != 1 {
		t.Fatalf("expected exactly one warning, got %d\noutput: %q", got, buf.String())
	}
}

func TestEnvVarSuppressesWarning(t *testing.T) {
	withLocalVersion(t, "1.72.0")
	t.Setenv(DisableVersionCheckEnv, "true")
	var buf bytes.Buffer
	warnOnceFromServerHeaderTo(&buf, "hoopgateway/1.73.0")
	if buf.Len() != 0 {
		t.Fatalf("env var should suppress warning, got %q", buf.String())
	}
}

func TestSuppressVersionWarning(t *testing.T) {
	withLocalVersion(t, "1.72.0")
	SuppressVersionWarning()
	var buf bytes.Buffer
	warnOnceFromServerHeaderTo(&buf, "hoopgateway/1.73.0")
	if buf.Len() != 0 {
		t.Fatalf("SuppressVersionWarning should silence output, got %q", buf.String())
	}
}
