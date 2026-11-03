package appconfig

import (
	"os"
	"path/filepath"
	"testing"
)

// HOOP_LICENSE semantics: a value that starts with "{" is the document, any
// other value is a path to it. A missing file or a value that is not a
// license document fails fast; the signature is checked at boot. It is
// refused with ORG_MULTI_TENANT.
func TestLoadLicense(t *testing.T) {
	doc := `{"payload":{"type":"enterprise","issued_at":1,"expire_at":4102444800,"allowed_hosts":["*"],"description":"d"},"key_id":"k","signature":"c2ln"}`
	path := filepath.Join(t.TempDir(), "license.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name        string
		env         string
		multiTenant string
		wantErr     bool
		wantData    string
	}{
		{"unset", "", "", false, ""},
		{"inline json", doc, "", false, doc},
		{"inline json with surrounding space", " " + doc + "\n", "", false, doc},
		{"file path", path, "", false, doc},
		{"missing file", filepath.Join(t.TempDir(), "nope.json"), "", true, ""},
		{"not a license document", `{"payload":`, "", true, ""},
		{"refused with multi tenant", doc, "true", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("POSTGRES_DB_URI", "postgres://u:p@localhost:5432/db")
			t.Setenv("HOOP_LICENSE", tc.env)
			t.Setenv("ORG_MULTI_TENANT", tc.multiTenant)
			runtimeConfig = Config{}

			err := Load()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected Load to fail, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load failed: %v", err)
			}
			if got := string(Get().LicenseData()); got != tc.wantData {
				t.Fatalf("LicenseData() = %q, want %q", got, tc.wantData)
			}
		})
	}
}

// ANALYTICS_MODE semantics: unset leaves the stored mode alone, otherwise it
// must be one of the three modes. It is refused with ORG_MULTI_TENANT.
func TestLoadAnalyticsMode(t *testing.T) {
	for _, tc := range []struct {
		name        string
		env         string
		multiTenant string
		wantErr     bool
	}{
		{"unset", "", "", false},
		{"identified", "identified", "", false},
		{"anonymous", "anonymous", "", false},
		{"disabled", "disabled", "", false},
		{"unknown mode", "enabled", "", true},
		{"wrong case", "Disabled", "", true},
		{"refused with multi tenant", "disabled", "true", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("POSTGRES_DB_URI", "postgres://u:p@localhost:5432/db")
			t.Setenv("ANALYTICS_MODE", tc.env)
			t.Setenv("ORG_MULTI_TENANT", tc.multiTenant)
			runtimeConfig = Config{}

			err := Load()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected Load to fail, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load failed: %v", err)
			}
			if got := Get().AnalyticsMode(); got != tc.env {
				t.Fatalf("AnalyticsMode() = %q, want %q", got, tc.env)
			}
		})
	}
}
