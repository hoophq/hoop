package sidecartui

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoophq/hoop/client/cmd/sidecardemo"
	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
)

func writeDemoConfig(t *testing.T, demoAddr string) string {
	t.Helper()
	cfg := &daemon.Config{Listeners: []daemon.ListenerConfig{{
		Name: "demo", Protocol: "http", Listen: "127.0.0.1:0", Upstream: demoAddr,
	}}}
	b, err := configyaml.Render(cfg, configyaml.RenderOptions{
		Extensions: []configyaml.Extension{{Key: configyaml.DemoAPIKey, Value: demoAddr}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), configyaml.StarterFile)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A demo config must bring its API up on every boot, not only the one the
// setup screen started, or `hoop start sidecar --config` on it later
// fronts nothing.
func TestStartDemoServesTheAPIAConfigNames(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	path := writeDemoConfig(t, addr)
	cfg, err := configyaml.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := StartDemo(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	ports, notes := targetPorts(d), d.notes
	if ports == nil || ports.API != addr || ports.Listen != cfg.Listeners[0].Listen {
		t.Errorf("ports = %+v, want the API %s and the listener in front of it", ports, addr)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], addr) {
		t.Errorf("notes = %v, want them to name %s", notes, addr)
	}
	resp, err := http.Get("http://" + addr + "/users")
	if err != nil {
		t.Fatalf("the demo API is not up: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func writePGDemoConfig(t *testing.T, dbAddr string) string {
	t.Helper()
	cfg := &daemon.Config{Listeners: []daemon.ListenerConfig{{
		Name: "demo", Protocol: "postgres", Listen: "127.0.0.1:0", Upstream: dbAddr,
	}}}
	b, err := configyaml.Render(cfg, configyaml.RenderOptions{
		Extensions: []configyaml.Extension{{Key: configyaml.DemoPostgresKey, Value: dbAddr}},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), configyaml.StarterFile)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The Postgres demo rides the same mechanism under its own key, but its
// database is hoop's, across the network: booting the config serves nothing
// here, refuses nothing for not being loopback, and turns the psql tour on
// for the listener in front of it.
func TestStartDemoFrontsTheRemoteDemoDatabase(t *testing.T) {
	path := writePGDemoConfig(t, sidecardemo.PGAddr)
	cfg, err := configyaml.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := StartDemo(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	if len(d.targets) != 1 || d.targets[0].Kind != sidecardemo.KindPostgres {
		t.Errorf("targets = %+v, want the Postgres demo", d.targets)
	}
	if p := targetPorts(d); p == nil || p.API != sidecardemo.PGAddr || p.Listen != cfg.Listeners[0].Listen {
		t.Errorf("ports = %+v, want the listener in front of %s", p, sidecardemo.PGAddr)
	}
	if len(d.notes) < 2 || !strings.Contains(d.notes[0], sidecardemo.PGAddr) || !strings.Contains(d.notes[1], "psql") {
		t.Errorf("notes = %v, want the database named and psql commands to try", d.notes)
	}
}

// The demo database's address reaches the commands the tour shows and copies,
// so a config that makes it anything but a plain host:port does not boot.
func TestStartDemoRefusesACraftedDemoDatabaseAddress(t *testing.T) {
	_, err := StartDemo(writePGDemoConfig(t, "$(touch pwned).example.com:5432"), &daemon.Config{})
	if err == nil || !strings.Contains(err.Error(), configyaml.DemoPostgresKey) {
		t.Errorf("err = %v, want a refusal naming %s", err, configyaml.DemoPostgresKey)
	}
}

// Booting or validating a Postgres demo config first writes the RDS CA its
// listener verifies the database with; any other config writes nothing.
func TestPrepareDemoWritesTheRDSCAForThePostgresDemoOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ca, err := sidecardemo.CAPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := PrepareDemo(writeDemoConfig(t, "127.0.0.1:18081")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ca); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the HTTP demo wrote %s (err %v)", ca, err)
	}
	if err := PrepareDemo(writePGDemoConfig(t, sidecardemo.PGAddr)); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(ca); err != nil || !bytes.Contains(b, []byte("BEGIN CERTIFICATE")) {
		t.Errorf("no CA bundle at %s after preparing the Postgres demo (err %v)", ca, err)
	}
}

func TestStartDemoRefusesANonLoopbackAddress(t *testing.T) {
	if _, err := StartDemo(writeDemoConfig(t, "0.0.0.0:18081"), &daemon.Config{}); err == nil {
		t.Fatal("the demo API was allowed to listen beyond loopback")
	}
}

// localhost is bound as 127.0.0.1, never as whatever the resolver maps the
// name to: the demo API serves this machine only.
func TestLoopbackBind(t *testing.T) {
	for in, want := range map[string]string{
		"localhost:18081": "127.0.0.1:18081", "127.0.0.1:18081": "127.0.0.1:18081", "[::1]:18081": "[::1]:18081",
	} {
		if got, err := loopbackBind(in); err != nil || got != want {
			t.Errorf("loopbackBind(%s) = %s, %v; want %s", in, got, err, want)
		}
	}
	for _, in := range []string{"0.0.0.0:18081", "10.0.0.5:18081", "example.com:18081", "nope"} {
		if _, err := loopbackBind(in); err == nil {
			t.Errorf("loopbackBind(%s) accepted a non-loopback address", in)
		}
	}
}

// With no listener in front of the demo API, Try it stays off: its
// requests would reach a default port and another service, or nothing.
func TestStartDemoWithoutAListenerForItHasNoTour(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	d, err := StartDemo(writeDemoConfig(t, addr), &daemon.Config{Listeners: []daemon.ListenerConfig{
		{Name: "other", Protocol: "http", Listen: "127.0.0.1:0", Upstream: "127.0.0.1:1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	if len(d.targets) > 0 {
		t.Errorf("tour targets = %+v with no listener forwarding to %s", d.targets, addr)
	}
	if len(d.notes) != 1 || !strings.Contains(d.notes[0], "no listener forwards to it") {
		t.Errorf("notes = %v, want them to say what is missing", d.notes)
	}
}

// A demo API that stops serving is reported, not dropped.
func TestADemoAPIFailureIsReported(t *testing.T) {
	d := &DemoRun{stop: func() {}}
	errc := make(chan error, 1)
	errc <- errors.New("accept: too many open files")
	d.watch(errc)
	if err := d.Err(); err == nil || !strings.Contains(err.Error(), "too many open files") {
		t.Errorf("err = %v", err)
	}
}

func TestStartDemoIgnoresAConfigWithoutTheKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(path, []byte("listeners: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := StartDemo(path, &daemon.Config{})
	if err != nil || d.notes != nil || len(d.targets) > 0 {
		t.Fatalf("notes %v, err %v; want nothing started", d.notes, err)
	}
	d.Stop()
}

// targetPorts is the one demo's ports, nil when the tour is off.
func targetPorts(d *DemoRun) *sidecardemo.Ports {
	if len(d.targets) != 1 {
		return nil
	}
	return &d.targets[0].Ports
}

// The setup screen's demo names both keys: one boot serves the API, fronts
// the remote database, and hands the tour both demos, each matched to the
// listener in front of it.
func TestStartDemoRunsEveryDemoAConfigNames(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	cfg := &daemon.Config{Listeners: []daemon.ListenerConfig{
		{Name: "demo-api", Protocol: "http", Listen: "127.0.0.1:18080", Upstream: addr},
		{Name: "demo-postgres", Protocol: "postgres", Listen: "127.0.0.1:15432", Upstream: sidecardemo.PGAddr},
	}}
	b, err := configyaml.Render(cfg, configyaml.RenderOptions{Extensions: []configyaml.Extension{
		{Key: configyaml.DemoAPIKey, Value: addr}, {Key: configyaml.DemoPostgresKey, Value: sidecardemo.PGAddr}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), configyaml.StarterFile)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := StartDemo(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	if len(d.targets) != 2 || d.targets[0].Kind != sidecardemo.KindHTTP || d.targets[0].Listener != "demo-api" ||
		d.targets[1].Kind != sidecardemo.KindPostgres || d.targets[1].Listener != "demo-postgres" {
		t.Fatalf("targets = %+v, want the API then the database, each with its listener", d.targets)
	}
	joined := strings.Join(d.notes, "\n")
	if !strings.Contains(joined, "curl") || !strings.Contains(joined, "psql") {
		t.Errorf("notes = %v, want commands for both demos", d.notes)
	}
	resp, err := http.Get("http://" + addr + "/users")
	if err != nil {
		t.Fatalf("the demo API is not up: %v", err)
	}
	resp.Body.Close()
}

// A JSON config that encoding/json reads but YAML refuses (a \/ escape)
// boots as it did before the demo key existed: it is never parsed as YAML.
func TestStartDemoLeavesAJSONConfigAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	doc := `{"listeners":[{"name":"api","protocol":"http","listen":"127.0.0.1:0","upstream":"http:\/\/127.0.0.1:1"}]}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := StartDemo(path, &daemon.Config{})
	if err != nil {
		t.Fatalf("err = %v, want a JSON config left alone", err)
	}
	d.Stop()
}

// The demo's curl commands are shown and copied to paste in a shell: a
// listener whose address is not a tcp loopback host:port never reaches them.
func TestStartDemoIgnoresAListenerItCannotQuote(t *testing.T) {
	addr := "127.0.0.1:18089"
	d, err := StartDemo(writeDemoConfig(t, addr), &daemon.Config{Listeners: []daemon.ListenerConfig{
		{Name: "sock", Protocol: "http", Network: "unix", Listen: "/tmp/a$(id)", Upstream: addr},
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Stop()
	if len(d.targets) > 0 {
		t.Errorf("tour targets = %+v, want no tour for a unix listener", d.targets)
	}
	for _, n := range d.notes {
		if strings.Contains(n, "$(id)") {
			t.Errorf("note carries the listener's shell text: %q", n)
		}
	}
}
