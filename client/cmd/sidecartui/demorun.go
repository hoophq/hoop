package sidecartui

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync"

	"github.com/hoophq/hoop/client/cmd/sidecardemo"
	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// The built-in demos a config written by the setup screen names. They are a
// playground for the TUI: the CLI's start command only calls PrepareDemo,
// StartDemo and the result's methods, and none of this runs for a config
// without a demo key.

// StartDemo turns on every built-in demo the config at path names. A config
// without a demo key, or no path (a config from the control plane), starts
// nothing; every method of the result is safe to call either way.
//
// configyaml.DemoAPIKey names the HTTP demo: the CLI serves the invented API
// at the key's address for as long as the sidecar runs. That address must be
// loopback, because the API answers anyone who reaches it, and invented data
// is still not something to expose.
//
// configyaml.DemoPostgresKey names the Postgres demo: the database is hoop's,
// across the network, and nothing is served here. The key's address says which
// listener fronts it, so the tour can run.
func StartDemo(path string, cfg *daemon.Config) (*DemoRun, error) {
	none := &DemoRun{stop: func() {}}
	if path == "" {
		return none, nil
	}
	// The setup screens write the demo key only into YAML. A JSON config
	// is not parsed as YAML: valid JSON (a \/ escape, a repeated key) can
	// fail that parse, and would then stop a boot that works without it.
	if !configyaml.IsYAML(path) {
		return none, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return none, err
	}
	apiAddr, api, err := configyaml.ExtensionValue(data, configyaml.DemoAPIKey)
	if err != nil {
		return none, err
	}
	pgAddr, pg, err := configyaml.ExtensionValue(data, configyaml.DemoPostgresKey)
	if err != nil {
		return none, err
	}
	if !api && !pg {
		return none, nil
	}
	// Check both before serving anything, so a bad key starts nothing.
	var bind string
	if api {
		if bind, err = loopbackBind(apiAddr); err != nil {
			return none, fmt.Errorf("%s: %w", configyaml.DemoAPIKey, err)
		}
	}
	if pg {
		if err := sidecardemo.CheckAddr(pgAddr); err != nil {
			return none, fmt.Errorf("%s: %w", configyaml.DemoPostgresKey, err)
		}
	}

	d := &DemoRun{stop: func() {}}
	if api {
		ctx, cancel := context.WithCancel(context.Background())
		errc, err := sidecardemo.Serve(ctx, bind)
		if err != nil {
			cancel()
			return none, err
		}
		d.stop, d.what = cancel, "demo API"
		go d.watch(errc)
		d.tour(cfg, sidecardemo.KindHTTP, apiAddr, "demo API served at "+apiAddr)
	}
	if pg {
		d.tour(cfg, sidecardemo.KindPostgres, pgAddr, "hoop's demo database is "+pgAddr)
	}
	return d, nil
}

// tour adds the demo at addr to the tour, through the listener that forwards
// to it, and the notes that say how to try it. Without such a listener there
// is nothing to tour: a default port would reach another service, or nothing
// at all.
func (d *DemoRun) tour(cfg *daemon.Config, kind sidecardemo.Kind, addr, where string) {
	for _, l := range cfg.Listeners {
		// Listen goes into the commands shown and copied: only a tcp
		// loopback address, never a socket path a config could fill with
		// shell syntax.
		if l.Network != "" && l.Network != "tcp" {
			continue
		}
		if _, err := loopbackBind(l.Listen); err != nil {
			continue
		}
		if l.Upstream != addr {
			continue
		}
		t := DemoTarget{Kind: kind, Ports: sidecardemo.Ports{API: addr, Listen: l.Listen},
			Listener: l.Name, Analyzer: l.Analyzer != nil}
		d.targets = append(d.targets, t)
		d.notes = append(d.notes, where+"; try, from another terminal:")
		for _, c := range sidecardemo.TryCommands(kind, t.Ports, t.Analyzer) {
			d.notes = append(d.notes, "  "+c)
		}
		return
	}
	d.notes = append(d.notes, where+", but no listener forwards to it: "+
		"set a listener's upstream to "+addr+" to try the demo")
}

// PrepareDemo puts on disk what a demo config needs before the sidecar
// loads it: for the Postgres demo, the Amazon RDS CA its listener verifies the
// database against (sidecardemo.EnsureCA). Any other config is left alone. A
// config that cannot be read or parsed is left alone too: loading it is what
// reports that, with its own message.
// It runs before the sidecar reads the config, so a demo config boots on every
// run, not only right after the setup screen wrote it.
func PrepareDemo(path string) error {
	if path == "" || !configyaml.IsYAML(path) {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if _, ok, err := configyaml.ExtensionValue(data, configyaml.DemoPostgresKey); err != nil || !ok {
		return nil
	}
	_, err = sidecardemo.EnsureCA()
	return err
}

// DemoRun is the demos a config names, and the upstream the CLI runs for the
// HTTP one (the demo API).
type DemoRun struct {
	stop func()
	// what names the upstream the CLI serves (the demo API), for the logs.
	what string
	// targets are the demos the tour runs, one per demo key a listener
	// forwards to; empty when none does and the tour stays off.
	targets []DemoTarget
	notes   []string

	mu     sync.Mutex
	failed error
}

// watch reports a demo API that stops serving while the sidecar runs. The
// log line reaches the dashboard's Logs and System; err() hands it to the
// run's result, so it is not lost when the screen closes.
func (d *DemoRun) watch(errc <-chan error) {
	err := <-errc
	if err == nil {
		return
	}
	d.mu.Lock()
	d.failed = err
	d.mu.Unlock()
	slog.Error("the "+d.what+" stopped serving; the demo listener has nothing to forward to", "error", err.Error())
}

// Err is why the demo API stopped while the sidecar ran, nil if it did not.
func (d *DemoRun) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failed == nil {
		return nil
	}
	return fmt.Errorf("the %s stopped: %w", d.what, d.failed)
}

// Stop shuts the demo API down.
func (d *DemoRun) Stop() { d.stop() }

// Notes are the lines the dashboard and the log show about the demos.
func (d *DemoRun) Notes() []string { return d.notes }

// Options turns the Try it section on, nil when no listener fronts a demo.
func (d *DemoRun) Options(openURL func(string) error) *DemoOptions {
	if len(d.targets) == 0 {
		return nil
	}
	return &DemoOptions{OpenURL: openURL, Targets: d.targets}
}

// loopbackBind is the address the demo API binds for addr, which must be
// this machine's: a literal loopback IP, or localhost bound as 127.0.0.1
// rather than whatever the resolver maps the name to.
func loopbackBind(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if host == "localhost" {
		return net.JoinHostPort("127.0.0.1", port), nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("%s is not a loopback address; the demo API only serves this machine", addr)
	}
	return addr, nil
}
