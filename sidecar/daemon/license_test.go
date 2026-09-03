package daemon

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/license/licensetest"
)

// newTestLogger captures what Run would print, at a level that keeps both
// info and warnings so a test can tell them apart.
func newTestLogger(w *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// minimalConfig is one valid lane and nothing else, so a license test fails
// on the license. It is left unclosed: each case appends its own fields.
const minimalConfig = `{"listeners":[{"protocol":"postgres","listen":":1","upstream":"h:5432"}]`

func setupWith(t *testing.T, cfgJSON, licenseFlag string) (*Config, error) {
	t.Helper()
	cfg, _, err := Setup(writeConfig(t, cfgJSON), nil, nil, licenseFlag)
	return cfg, err
}

func TestSetupWithoutALicenseRunsTheFreeTier(t *testing.T) {
	t.Setenv(license.EnvVar, "")

	cfg, err := setupWith(t, minimalConfig+`}`, "")
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if got := cfg.Licensing().State(); got != license.StateMissing {
		t.Errorf("state = %q, want missing", got)
	}
	if !strings.Contains(LimitsSummary(cfg.Licensing()), "1 guardrail rule(s)") {
		t.Error("an unlicensed process did not report the free-tier caps")
	}
}

// The three sources in the order the spec sets. Every candidate is a path
// that does not resolve and the assertion is on which one the error names,
// which tests precedence without a signature: sidecar/license owns whether a
// document verifies, and this owns which document we picked.
func TestSetupPrefersTheFlagThenTheEnvironmentThenTheFile(t *testing.T) {
	cases := []struct {
		name       string
		flag, env  string
		file       string
		wantSource string
	}{
		{"flag wins", "/no/flag.json", "/no/env.json", "/no/file.json", "the license flag"},
		{"env beats the file", "", "/no/env.json", "/no/file.json", license.EnvVar},
		{"the file is the fallback", "", "", "/no/file.json", `the "license" config key`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(license.EnvVar, tc.env)
			cfgJSON := minimalConfig + `,"license":` + quote(tc.file) + `}`

			_, err := setupWith(t, cfgJSON, tc.flag)
			if err == nil {
				t.Fatal("a license that cannot be read was accepted")
			}
			if !strings.Contains(err.Error(), tc.wantSource) {
				t.Errorf("error names the wrong source: %v", err)
			}
		})
	}
}

// A license nobody can read stops startup, or the operator gets a process
// that dropped to the free tier and said nothing. An EXPIRED one keeps
// running capped, so Setup tests StateInvalid and nothing else.
func TestSetupRefusesALicenseItCannotRead(t *testing.T) {
	t.Setenv(license.EnvVar, "")
	cfgJSON := minimalConfig + `,"license":"/etc/hoop/nope.json"}`

	_, err := setupWith(t, cfgJSON, "")
	if err == nil {
		t.Fatal("an unreadable license was accepted")
	}
	msg := err.Error()
	for _, want := range []string{"/etc/hoop/nope.json", "paste the document"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message is missing %q: %s", want, msg)
		}
	}
	// The "not a stack trace" requirement. A refusal an operator cannot act
	// on costs a support ticket.
	if strings.Contains(msg, "goroutine") || strings.Contains(msg, ".go:") {
		t.Errorf("the message reads like a dump: %s", msg)
	}
}

// A value starting with "{" is the document, not a filename. A deployment
// that moves a license out of a mounted file and into a Helm value must not
// get "no such file or directory" naming its own JSON.
func TestAnInlineDocumentIsReadAsADocument(t *testing.T) {
	t.Setenv(license.EnvVar, "")
	doc := unsignedLicense(t)
	cfgJSON := minimalConfig + `,"license":` + quote(doc) + `}`

	_, err := setupWith(t, cfgJSON, "")
	if err == nil {
		t.Fatal("an unsigned license was accepted")
	}
	if strings.Contains(err.Error(), "cannot read the license file") {
		t.Errorf("the document was treated as a path: %v", err)
	}
	if !strings.Contains(err.Error(), "not valid") {
		t.Errorf("the document was not verified: %v", err)
	}
}

// -validate is where an operator finds out what a config does, so the license
// belongs in its report rather than only in the run log.
func TestPrintLanesReportsTheLicenseAndTheCaps(t *testing.T) {
	var buf bytes.Buffer
	PrintLanes(&buf, licensed(t), []LaneInfo{{Name: "appdb", Protocol: "postgres"}})

	out := buf.String()
	for _, want := range []string{"license: valid", "Acme Corp", "unlimited guardrail rule(s)", "appdb"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report is missing %q:\n%s", want, out)
		}
	}
}

func TestPrintLanesSaysWhenThereIsNoLicense(t *testing.T) {
	var buf bytes.Buffer
	PrintLanes(&buf, license.Status{}, nil)

	out := buf.String()
	for _, want := range []string{"license: missing", license.EnvVar, "1 guardrail rule(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report is missing %q:\n%s", want, out)
		}
	}
}

// An expired license reports as a warning and a valid one as information,
// because only one of them is something an operator has to act on.
func TestReportLicenseWarnsOnlyWhenSomethingIsWrong(t *testing.T) {
	expired := licensetest.Status(t, licensetest.Expiring(-time.Hour))
	cases := []struct {
		name     string
		lic      license.Status
		wantWarn bool
	}{
		{"valid", licensed(t), false},
		{"missing", license.Status{}, false},
		{"expired", expired, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			reportLicense(newTestLogger(&buf), tc.lic)
			out := buf.String()
			if got := strings.Contains(out, "level=WARN"); got != tc.wantWarn {
				t.Errorf("warned = %v, want %v: %s", got, tc.wantWarn, out)
			}
			if !strings.Contains(out, "license:") {
				t.Errorf("nothing was reported: %s", out)
			}
		})
	}
}

// quote renders s as a JSON string, so a path or a document full of quotes
// survives being embedded in a config.
func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// unsignedLicense is a complete license document carrying a signature this
// build does not trust. It cannot be signed for real here: the trusted key is
// unexported in sidecar/license, whose own tests swap it and cover the
// signature. These tests are about what the daemon does with the verdict.
func unsignedLicense(t *testing.T) string {
	t.Helper()
	l := license.License{
		Payload: license.Payload{
			Type:         license.EnterpriseType,
			IssuedAt:     time.Now().Add(-time.Hour).Unix(),
			ExpireAt:     time.Now().Add(time.Hour).Unix(),
			AllowedHosts: []string{"*"},
			Description:  "Acme Corp",
		},
		KeyID:     "test-key",
		Signature: "bm90LWEtc2lnbmF0dXJl",
	}
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("marshal license: %v", err)
	}
	return string(b)
}
