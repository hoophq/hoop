package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/hoophq/hoop/client/cmd/sidecardemo"
)

// Only a listener that forwards to the demo's own upstream has its holds filed
// at the terminal under a plane: never a listener of a config without the demo
// key, and never one that forwards elsewhere.
func TestSidecarDemoListenersNamesOnlyTheDemoLane(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	pg := sidecardemo.PGAddr
	demo := write("demo.yaml", "x-hoop-demo-postgres: "+pg+"\n"+
		"listeners:\n"+
		"  - {name: demo, protocol: postgres, listen: 127.0.0.1:18082, upstream: "+pg+"}\n"+
		"  - {name: prod, protocol: postgres, listen: 127.0.0.1:15432, upstream: 10.0.0.5:5432}\n")
	if got := sidecarDemoListeners(demo); !slices.Equal(got, []string{"demo"}) {
		t.Errorf("demo config: %v, want [demo]", got)
	}

	plain := write("plain.yaml", "listeners:\n"+
		"  - {name: demo, protocol: postgres, listen: 127.0.0.1:18082, upstream: "+pg+"}\n")
	if got := sidecarDemoListeners(plain); got != nil {
		t.Errorf("a config without the demo key: %v, want none", got)
	}

	other := write("other.yaml", "x-hoop-demo-postgres: 10.0.0.5:5432\n"+
		"listeners:\n"+
		"  - {name: demo, protocol: postgres, listen: 127.0.0.1:18082, upstream: 10.0.0.5:5432}\n")
	if got := sidecarDemoListeners(other); got != nil {
		t.Errorf("a demo key naming another database: %v, want none", got)
	}

	api := write("api.yaml", "x-hoop-demo-api: 127.0.0.1:18081\n"+
		"listeners:\n"+
		"  - {name: demo, protocol: http, listen: 127.0.0.1:18080, upstream: 127.0.0.1:18081}\n")
	if got := sidecarDemoListeners(api); !slices.Equal(got, []string{"demo"}) {
		t.Errorf("HTTP demo config: %v, want [demo]", got)
	}
	remote := write("remote.yaml", "x-hoop-demo-api: 10.0.0.9:80\n"+
		"listeners:\n"+
		"  - {name: demo, protocol: http, listen: 127.0.0.1:18080, upstream: 10.0.0.9:80}\n")
	if got := sidecarDemoListeners(remote); got != nil {
		t.Errorf("an HTTP demo key off loopback: %v, want none", got)
	}
	if sidecarDemoListeners("") != nil || sidecarDemoListeners(filepath.Join(dir, "missing.yaml")) != nil {
		t.Error("no config named a demo lane")
	}
}
