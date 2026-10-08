package yaml_test

import (
	"strings"
	"testing"

	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// The starter is a user's first config: it must load for every protocol it
// claims to front, whatever the inference found. A starter that fails to
// load turns the first run's next step into an error.
func TestStarterLoadsForEveryStarterProtocol(t *testing.T) {
	addrs := map[string]string{
		"postgres": "127.0.0.1:5432", "mysql": "127.0.0.1:3306", "mssql": "127.0.0.1:1433",
		"oracle": "127.0.0.1:1521", "clickhouse": "127.0.0.1:8123", "mongodb": "127.0.0.1:27017",
		"http": "127.0.0.1:8080",
	}
	for p, addr := range addrs {
		if !configyaml.StarterProtocol(p) {
			t.Fatalf("%s is not a starter protocol", p)
		}
		for _, provider := range []string{"", "anthropic", "openai", "vertex"} {
			b, err := configyaml.Starter(configyaml.StarterInput{
				Primary:          configyaml.Upstream{Protocol: p, Addr: addr, Source: "test"},
				Others:           []configyaml.Upstream{{Protocol: "mysql", Addr: "127.0.0.1:3306", Source: "port 3306 is open"}},
				AnalyzerProvider: provider,
			})
			if err != nil {
				t.Fatalf("Starter(%s, %q): %v", p, provider, err)
			}
			cfg, err := configyaml.LoadYAMLBytes(b)
			if err != nil {
				t.Fatalf("Starter(%s, %q) does not load: %v\n%s", p, provider, err, b)
			}
			if len(cfg.Listeners) != 1 {
				t.Errorf("Starter(%s): %d listeners, want 1 (others stay commented)", p, len(cfg.Listeners))
			}
			// Commented out, always: the key lives in a file the starter
			// never writes.
			if cfg.Analyzer != nil && cfg.Analyzer.Provider != "" {
				t.Errorf("Starter(%s, %q) turned the analyzer on", p, provider)
			}
		}
	}
}

func TestStarterListsEveryProtocol(t *testing.T) {
	b, err := configyaml.Starter(configyaml.StarterInput{
		Primary: configyaml.Upstream{Protocol: "postgres", Addr: "127.0.0.1:5432", Source: "default"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range daemon.Protocols() {
		if !strings.Contains(string(b), "#   "+p+" ") {
			t.Errorf("the starter header does not list protocol %q", p)
		}
	}
	if !strings.Contains(string(b), "listen: 127.0.0.1:15432") {
		t.Errorf("postgres on 5432 should listen on 127.0.0.1:15432:\n%s", b)
	}
}

func TestStarterRefusesAProtocolItCannotFill(t *testing.T) {
	for _, p := range []string{"grpc", "spanner", "ssh", "nope"} {
		if _, err := configyaml.Starter(configyaml.StarterInput{
			Primary: configyaml.Upstream{Protocol: p, Addr: "127.0.0.1:9000"},
		}); err == nil {
			t.Errorf("Starter(%s) succeeded; it cannot fill that protocol's block", p)
		}
	}
}
