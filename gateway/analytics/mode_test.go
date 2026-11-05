package analytics

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
)

func resetModeCache(t *testing.T) {
	t.Helper()
	modeCacheMu.Lock()
	modeCache = map[string]string{}
	modeCacheMu.Unlock()
}

func TestGetMode(t *testing.T) {
	t.Run("missing org falls back to anonymous", func(t *testing.T) {
		resetModeCache(t)
		if got := GetMode("unknown"); got != models.AnalyticsModeAnonymous {
			t.Fatalf("got %q, want %q", got, models.AnalyticsModeAnonymous)
		}
	})

	t.Run("empty orgID falls back to anonymous", func(t *testing.T) {
		resetModeCache(t)
		if got := GetMode(""); got != models.AnalyticsModeAnonymous {
			t.Fatalf("got %q, want %q", got, models.AnalyticsModeAnonymous)
		}
	})

	t.Run("Set persists for subsequent Get", func(t *testing.T) {
		resetModeCache(t)
		SetMode("org-1", models.AnalyticsModeIdentified)
		if got := GetMode("org-1"); got != models.AnalyticsModeIdentified {
			t.Fatalf("got %q, want identified", got)
		}
		SetMode("org-1", models.AnalyticsModeDisabled)
		if got := GetMode("org-1"); got != models.AnalyticsModeDisabled {
			t.Fatalf("got %q, want disabled", got)
		}
	})

	t.Run("Set rejects invalid modes", func(t *testing.T) {
		resetModeCache(t)
		SetMode("org-1", "bogus")
		if got := GetMode("org-1"); got != models.AnalyticsModeAnonymous {
			t.Fatalf("got %q, want fallback anonymous", got)
		}
	})

	t.Run("Set with empty orgID is a no-op", func(t *testing.T) {
		resetModeCache(t)
		SetMode("", models.AnalyticsModeIdentified)
		modeCacheMu.RLock()
		size := len(modeCache)
		modeCacheMu.RUnlock()
		if size != 0 {
			t.Fatalf("expected empty cache, got %d entries", size)
		}
	})
}

// The enterprise branch needs a document signed with the trusted key, which
// common/license's own tests cover; an unsigned enterprise document is free.
func TestModeAtBoot(t *testing.T) {
	unsigned := []byte(`{"payload":{"type":"enterprise"},"signature":"x"}`)

	for _, tc := range []struct {
		name     string
		tracking string
		license  []byte
		want     string
	}{
		{"enabled wins", "enabled", unsigned, models.AnalyticsModeIdentified},
		{"disabled wins", "disabled", nil, models.AnalyticsModeDisabled},
		{"no license is free", "", nil, models.AnalyticsModeIdentified},
		{"unverified license is free", "", unsigned, models.AnalyticsModeIdentified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ModeAtBoot(tc.tracking, tc.license, "localhost"); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
