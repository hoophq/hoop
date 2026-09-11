package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestStartSidecarCommandNames(t *testing.T) {
	for _, name := range []string{"sidecar", "inspect"} {
		t.Run(name, func(t *testing.T) {
			got, _, err := startCmd.Find([]string{name})
			if err != nil {
				t.Fatalf("resolving %q: %v", name, err)
			}
			if got != startSidecarCmd {
				t.Fatalf("%q resolved to %q, want the sidecar command", name, got.Name())
			}
		})
	}
}

func TestWarnDeprecatedSidecarAlias(t *testing.T) {
	for _, tt := range []struct {
		msg      string
		calledAs string
		wantWarn bool
	}{
		{msg: "the old name warns", calledAs: "inspect", wantWarn: true},
		{msg: "the new name is silent", calledAs: "sidecar"},
		{msg: "an empty invocation name is silent", calledAs: ""},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			var buf bytes.Buffer
			warnDeprecatedSidecarAlias(&buf, tt.calledAs)

			got := buf.String()
			if !tt.wantWarn {
				if got != "" {
					t.Fatalf("want no output, got %q", got)
				}
				return
			}
			if !strings.Contains(got, "hoop start sidecar") {
				t.Fatalf("warning does not name the new command: %q", got)
			}
			if !strings.Contains(got, "deprecated") {
				t.Fatalf("warning does not say deprecated: %q", got)
			}
		})
	}
}

func TestSidecarConfigFromEnv(t *testing.T) {
	for _, tt := range []struct {
		msg     string
		sidecar string
		inspect string
		want    string
	}{
		{msg: "the current name wins", sidecar: "/new.yaml", inspect: "/old.yaml", want: "/new.yaml"},
		{msg: "the pre-rename name still works", inspect: "/old.yaml", want: "/old.yaml"},
		{msg: "an empty current name falls back", sidecar: "", inspect: "/old.yaml", want: "/old.yaml"},
		{msg: "neither set resolves to empty"},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			t.Setenv("HOOP_SIDECAR_CONFIG", tt.sidecar)
			t.Setenv("HOOP_INSPECT_CONFIG", tt.inspect)

			if got := sidecarConfigFromEnv(); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSidecarBareInvocation(t *testing.T) {
	// The gate reads package-level flag variables; save and restore them so
	// this test composes with any other that touches the command.
	restore := func(validate, strict bool, lic, tok string) {
		sidecarValidateFlag, sidecarStrictFlag = validate, strict
		sidecarLicenseFlag, sidecarTokenFlag = lic, tok
	}
	defer restore(sidecarValidateFlag, sidecarStrictFlag, sidecarLicenseFlag, sidecarTokenFlag)

	for _, tt := range []struct {
		msg      string
		validate bool
		strict   bool
		license  string
		token    string
		want     bool
	}{
		{msg: "nothing at all is bare", want: true},
		{msg: "--validate keeps the usage error", validate: true},
		{msg: "--strict keeps the usage error", strict: true},
		{msg: "--license keeps the usage error", license: "/lic.json"},
		{msg: "--token keeps the usage error", token: "hsc_x"},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			restore(tt.validate, tt.strict, tt.license, tt.token)
			if got := sidecarBareInvocation(); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
