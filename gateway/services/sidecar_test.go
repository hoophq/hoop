package services

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestParseSidecarConfiguration(t *testing.T) {
	for _, tt := range []string{"", "{}"} {
		cfg, err := ParseSidecarConfiguration(json.RawMessage(tt))
		if err != nil {
			t.Fatalf("raw=%q: unexpected error: %v", tt, err)
		}
		if len(cfg.Listeners) != 0 {
			t.Errorf("raw=%q: want no listeners, got %d", tt, len(cfg.Listeners))
		}
		if cfg.LogLevel != "" || cfg.Audit.File != "" || cfg.Admin.Listen != "" {
			t.Errorf("raw=%q: gateway must not inject defaults, got log_level=%q audit.file=%q admin.listen=%q",
				tt, cfg.LogLevel, cfg.Audit.File, cfg.Admin.Listen)
		}
	}

	raw := `{"log_level":"debug","audit":{"file":"-"},"listeners":[{"name":"pg","protocol":"postgres","listen":"0.0.0.0:5432","upstream":"db:5432"}]}`
	cfg, err := ParseSidecarConfiguration(json.RawMessage(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("want log_level=debug, got %q", cfg.LogLevel)
	}
	if cfg.Audit.File != "-" {
		t.Errorf("want audit.file=-, got %q", cfg.Audit.File)
	}
	if len(cfg.Listeners) != 1 {
		t.Fatalf("want one listener, got %d", len(cfg.Listeners))
	}
	l := cfg.Listeners[0]
	if l.Name != "pg" || l.Protocol != "postgres" || l.Listen != "0.0.0.0:5432" || l.Upstream != "db:5432" {
		t.Errorf("listener not decoded: %+v", l)
	}

	if _, err := ParseSidecarConfiguration(json.RawMessage(`null`)); err == nil {
		t.Error("want error for an explicit null document, got nil")
	}

	if _, err := ParseSidecarConfiguration(json.RawMessage(`{"listners":[]}`)); err == nil {
		t.Error("want error for unknown field, got nil")
	}
}

// The control plane authors configurations for hosts it cannot see. A listener
// it stores without checking becomes a sidecar that refuses to boot on the
// customer's infrastructure, where nobody watching the UI can read the error.
func TestParseSidecarConfigurationRejectsUnusableListeners(t *testing.T) {
	for _, tt := range []struct{ name, raw, want string }{
		{"no protocol",
			`{"listeners":[{"name":"a","listen":":1","upstream":"h:1"}]}`,
			"a: no protocol"},
		{"unknown protocol",
			`{"listeners":[{"name":"a","protocol":"redis","listen":":1","upstream":"h:1"}]}`,
			`a: unsupported protocol "redis"`},
		{"no listen",
			`{"listeners":[{"name":"a","protocol":"postgres","upstream":"h:1"}]}`,
			"a: no listen address"},
		{"no upstream",
			`{"listeners":[{"name":"a","protocol":"postgres","listen":":1"}]}`,
			"a: no upstream"},
		{"bad network",
			`{"listeners":[{"name":"a","protocol":"postgres","listen":":1","upstream":"h:1","network":"udp"}]}`,
			`a: network must be tcp or unix, got "udp"`},
		{"duplicate listen",
			`{"listeners":[
				{"name":"a","protocol":"postgres","listen":":1","upstream":"h:1"},
				{"name":"b","protocol":"postgres","listen":":1","upstream":"h:2"}]}`,
			`b: duplicate listen address ":1"`},
		{"downstream_tls on a lane that cannot terminate it",
			`{"listeners":[{"name":"a","protocol":"mysql","listen":":1","upstream":"h:1",
				"downstream_tls":{"cert_file":"/c.pem","key_file":"/k.pem"}}]}`,
			"a: downstream_tls is only supported on postgres, grpc and spanner"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseSidecarConfiguration(json.RawMessage(tt.raw))
			if err == nil {
				t.Fatal("an unusable listener was accepted")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error does not say %q:\n%v", tt.want, err)
			}
		})
	}
}

// The protocol list comes from the codec registry, which is populated by a
// blank import. Without it inspect.New knows nothing and every listener is
// refused — and the failure looks like a bad request, not a missing import.
func TestParseSidecarConfigurationAcceptsEverySupportedProtocol(t *testing.T) {
	for i, proto := range []string{"postgres", "mysql", "mssql", "mongodb", "http", "grpc", "spanner"} {
		raw := fmt.Sprintf(`{"listeners":[{"name":%q,"protocol":%q,"listen":":%d","upstream":"h:1"}]}`,
			proto, proto, 5000+i)
		if _, err := ParseSidecarConfiguration(json.RawMessage(raw)); err != nil {
			t.Errorf("protocol %q was refused: %v", proto, err)
		}
	}
}

// Nothing the sidecar's own loader accepts may be refused here, or an existing
// deployment stops being able to seed the plane from its file.
func TestParseSidecarConfigurationAcceptsDeprecatedSpellings(t *testing.T) {
	raw := `{"listeners":[{
		"connection":"pg",
		"protocol":"postgres",
		"listen":":1",
		"upstream":"h:1",
		"policy":{"rules":[{"name":"r","type":"operation","operations":["drop"]}]},
		"mask":{"enabled":true}
	}]}`
	if _, err := ParseSidecarConfiguration(json.RawMessage(raw)); err != nil {
		t.Errorf("a config in the deprecated spelling was refused: %v", err)
	}
}

// Paths in a configuration name files on the SIDECAR's host. The gateway
// cannot see them and must not try: refusing them would refuse every real
// config that uses TLS.
func TestParseSidecarConfigurationAcceptsPathsItCannotSee(t *testing.T) {
	raw := `{"listeners":[{
		"name":"pg","protocol":"postgres","listen":":1","upstream":"h:1",
		"downstream_tls":{"cert_file":"/etc/hoop-inspect/tls.crt","key_file":"/etc/hoop-inspect/tls.key"},
		"upstream_tls":{"ca_file":"/etc/hoop-inspect/ca.pem"}
	}]}`
	if _, err := ParseSidecarConfiguration(json.RawMessage(raw)); err != nil {
		t.Errorf("a config naming paths on the sidecar host was refused: %v", err)
	}
}

// A sidecar is created before it is configured, so POST /sidecars with no
// document has to keep working. ImportConfiguration owns the "seed at least
// one listener" rule on its own.
func TestParseSidecarConfigurationAcceptsNoListeners(t *testing.T) {
	for _, raw := range []string{"", "{}", `{"log_level":"debug"}`} {
		if _, err := ParseSidecarConfiguration(json.RawMessage(raw)); err != nil {
			t.Errorf("raw=%q was refused: %v", raw, err)
		}
	}
}
