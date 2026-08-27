package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/controlplane/backend/internal/config"
)

const validURI = "postgres://hoop:hoop@localhost:5432/hoop?sslmode=disable"

// An external test package, so these cases can only reach what a caller can
// reach. Load returns a value and there is no global to reset between them.

func TestLoadDefaults(t *testing.T) {
	t.Setenv("POSTGRES_DB_URI", validURI)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	if cfg.ListenAddr != "0.0.0.0:8020" {
		t.Errorf("ListenAddr = %q, want 0.0.0.0:8020", cfg.ListenAddr)
	}
	if cfg.Deployment != config.DeploymentDevelopment {
		t.Errorf("Deployment = %q, want development", cfg.Deployment)
	}
	if cfg.IsProduction() {
		t.Error("IsProduction is true with no deployment set; an unset variable must not unlock production behaviour")
	}
	if cfg.ShutdownGrace != 15*time.Second {
		t.Errorf("ShutdownGrace = %s, want 15s", cfg.ShutdownGrace)
	}
	if !cfg.AutoMigrate {
		t.Error("AutoMigrate is false by default; a first run would start against an empty schema")
	}
	if len(cfg.CORSAllowedOrigins) != 0 {
		t.Errorf("CORSAllowedOrigins = %v, want empty so no origin is allowed by default", cfg.CORSAllowedOrigins)
	}
}

func TestLoadRequiresPostgresURI(t *testing.T) {
	t.Setenv("POSTGRES_DB_URI", "")
	if _, err := config.Load(); err == nil {
		t.Error("Load succeeded with no POSTGRES_DB_URI")
	}
}

// url.Parse alone accepts all of these. They then reach the driver and
// produce a message that never names the variable.
func TestLoadRejectsANonPostgresURI(t *testing.T) {
	for _, uri := range []string{
		"postgress://localhost/hoop",
		"localhost:5432/hoop",
		"host=localhost user=hoop",
		"mysql://localhost/hoop",
	} {
		t.Run(uri, func(t *testing.T) {
			t.Setenv("POSTGRES_DB_URI", uri)
			_, err := config.Load()
			if err == nil {
				t.Fatalf("Load accepted %q", uri)
			}
			if !strings.Contains(err.Error(), "POSTGRES_DB_URI") {
				t.Errorf("error does not name the variable: %v", err)
			}
		})
	}
}

// url.Error stringifies as `parse "<the whole URL>": <cause>` with no
// redaction, so a password containing an unescaped % ends up in the log of
// every failed start.
func TestLoadDoesNotLeakThePasswordOnAParseError(t *testing.T) {
	t.Setenv("POSTGRES_DB_URI", "postgres://hoop:p%ssw0rd@localhost:5432/hoop")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load accepted a URI with an invalid escape")
	}
	if strings.Contains(err.Error(), "ssw0rd") {
		t.Errorf("the credential reached the error message: %v", err)
	}
}

func TestLoadRejectsAnUnknownDeployment(t *testing.T) {
	t.Setenv("POSTGRES_DB_URI", validURI)
	t.Setenv("CONTROLPLANE_DEPLOYMENT", "staging")
	if _, err := config.Load(); err == nil {
		t.Error("Load accepted an unknown deployment")
	}
}

func TestLoadReadsProduction(t *testing.T) {
	t.Setenv("POSTGRES_DB_URI", validURI)
	t.Setenv("CONTROLPLANE_DEPLOYMENT", "PRODUCTION")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	if !cfg.IsProduction() {
		t.Error("IsProduction is false with CONTROLPLANE_DEPLOYMENT=PRODUCTION")
	}
}

func TestLoadParsesCORSOrigins(t *testing.T) {
	t.Setenv("POSTGRES_DB_URI", validURI)
	t.Setenv("CONTROLPLANE_CORS_ALLOWED_ORIGINS", " http://localhost:5173 , https://admin.example.com ,")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	want := []string{"http://localhost:5173", "https://admin.example.com"}
	if len(cfg.CORSAllowedOrigins) != len(want) {
		t.Fatalf("CORSAllowedOrigins = %v, want %v", cfg.CORSAllowedOrigins, want)
	}
	for i := range want {
		if cfg.CORSAllowedOrigins[i] != want[i] {
			t.Errorf("origin %d = %q, want %q", i, cfg.CORSAllowedOrigins[i], want[i])
		}
	}
}

func TestLoadRejectsMalformedNumbersAndDurations(t *testing.T) {
	cases := map[string][2]string{
		"negative conns":       {"CONTROLPLANE_MAX_OPEN_CONNS", "-1"},
		"non-numeric conns":    {"CONTROLPLANE_MAX_OPEN_CONNS", "many"},
		"zero grace":           {"CONTROLPLANE_SHUTDOWN_GRACE", "0s"},
		"unparseable grace":    {"CONTROLPLANE_SHUTDOWN_GRACE", "soon"},
		"non-bool automigrate": {"CONTROLPLANE_AUTO_MIGRATE", "maybe"},
	}
	for name, kv := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("POSTGRES_DB_URI", validURI)
			t.Setenv(kv[0], kv[1])
			if _, err := config.Load(); err == nil {
				t.Errorf("Load accepted %s=%s", kv[0], kv[1])
			}
		})
	}
}
