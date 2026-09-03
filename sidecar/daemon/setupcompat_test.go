package daemon

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/license/licensetest"
)

// setupSignature is the EXACT shape the README has told embedders to call
// since before licensing existed, variadic included, which is to say not
// variadic at all.
//
// Assigning Setup to it is the whole test, and it is a compile-time one: add
// a parameter, or turn it variadic, and this file stops building. That is the
// failure an embedder holding Setup in a typed variable would otherwise
// discover on upgrade. The license went in as a fourth argument once and
// broke every caller; a variadic would have broken this one.
type setupSignature func(string, Loader, PluginBuilder) (*Config, Plugin, error)

var _ setupSignature = Setup

// The three-argument call still compiles and still works. Options are
// additions, so a caller that wants none passes none.
func TestSetupTakesThreeArgumentsAsBefore(t *testing.T) {
	t.Setenv(license.EnvVar, "")

	cfg, det, err := Setup(writeConfig(t, minimalConfig+`}`), nil, nil)
	if err != nil {
		t.Fatalf("the three-argument call failed: %v", err)
	}
	if cfg == nil {
		t.Fatal("no config came back")
	}
	if det != nil {
		t.Error("a nil PluginBuilder produced a detector")
	}
	if cfg.Licensing().State() != license.StateMissing {
		t.Errorf("state = %q, want missing", cfg.Licensing().State())
	}
}

// An embedder who never learned about the option still gets a license from
// the environment and from the config file. Only the command-line source
// needs WithLicense, because only a caller with a flag has one.
func TestTheThreeArgumentCallStillReadsTheOtherTwoSources(t *testing.T) {
	doc := licensetest.Document(t, licensetest.Enterprise())

	t.Run("from the environment", func(t *testing.T) {
		t.Setenv(license.EnvVar, doc)
		cfg, _, err := Setup(writeConfig(t, minimalConfig+`}`), nil, nil)
		if err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if cfg.Licensing().State() != license.StateValid {
			t.Errorf("state = %q, want valid", cfg.Licensing().State())
		}
	})

	t.Run("from the config file", func(t *testing.T) {
		t.Setenv(license.EnvVar, "")
		cfgJSON := minimalConfig + `,"license":` + quote(doc) + `}`
		cfg, _, err := Setup(writeConfig(t, cfgJSON), nil, nil)
		if err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if cfg.Licensing().State() != license.StateValid {
			t.Errorf("state = %q, want valid", cfg.Licensing().State())
		}
	})
}

// WithLicense is the command-line source, and it outranks the other two.
func TestWithLicenseOutranksTheEnvironment(t *testing.T) {
	t.Setenv(license.EnvVar, "/no/env.json")

	_, _, err := SetupWith(writeConfig(t, minimalConfig+`}`), nil, nil,
		WithLicense("/no/flag.json"))

	if err == nil {
		t.Fatal("a license that cannot be read was accepted")
	}
	if !strings.Contains(err.Error(), "the license flag") {
		t.Errorf("the option did not win: %v", err)
	}
}

// An empty WithLicense is the same as passing no option, so a caller can hand
// through a flag variable without branching on whether the operator set it.
func TestAnEmptyWithLicenseFallsThrough(t *testing.T) {
	t.Setenv(license.EnvVar, licensetest.Document(t, licensetest.Enterprise()))

	cfg, _, err := SetupWith(writeConfig(t, minimalConfig+`}`), nil, nil, WithLicense(""))
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if cfg.Licensing().Source != license.EnvVar {
		t.Errorf("source = %q, want %q", cfg.Licensing().Source, license.EnvVar)
	}
}

// Options apply in order, so a caller assembling them from several places
// gets last-wins rather than a silent merge.
func TestTheLastOptionWins(t *testing.T) {
	t.Setenv(license.EnvVar, "")

	_, _, err := SetupWith(writeConfig(t, minimalConfig+`}`), nil, nil,
		WithLicense("/no/first.json"), WithLicense("/no/second.json"))

	if err == nil {
		t.Fatal("a license that cannot be read was accepted")
	}
	if !strings.Contains(err.Error(), "/no/second.json") {
		t.Errorf("the last option did not win: %v", err)
	}
}
