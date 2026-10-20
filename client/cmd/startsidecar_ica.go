package cmd

import (
	"errors"
	"fmt"

	"github.com/hoophq/hoop/sidecar/daemon"
)

// hoop carries no ICA engine. The engine's NER runs on ONNX Runtime, a cgo
// backend, and hoop is a static binary that cross-compiles for every
// platform; linking the engine would also make every Linux hoop — the agent
// and slim sidecar images included — advertise protocol:ica to a control
// plane and receive lanes it has no worker, model or tesseract for. So the
// engine lives in hoop-inspect alone, built with -tags ORT and shipped in
// the ica assets bundle (deploy/ica-sidecar/), and `hoop start sidecar` on a
// config with an ica lane hands the process over to it: same config, same
// flags, same PID, so a systemd unit or a container entrypoint that runs
// `hoop start sidecar` serves the lane once the bundle is installed.

// hoopInspectName is the binary looked up on PATH.
const hoopInspectName = "hoop-inspect"

// hoopInspectEnv names the binary outright, ahead of PATH.
const hoopInspectEnv = "HOOP_INSPECT"

// hoopInspectPaths are tried after PATH: where deploy/ica-sidecar/install.sh
// puts the binary, and where the hoopsidecar -ica image carries it.
var hoopInspectPaths = []string{"/usr/local/bin/hoop-inspect", "/app/hoop-inspect"}

// icaHandoff decides whether this run must be served by hoop-inspect and
// finds it. It returns "" when the config has no ica lane or this build
// links the engine, the lane names otherwise, and an error naming the
// bundle when the binary is nowhere to be found.
func icaHandoff(cfg *daemon.Config, getenv func(string) string, lookPath func(string) (string, error), exists func(string) bool) (string, []string, error) {
	lanes := cfg.ICALanes()
	if len(lanes) == 0 || daemon.ICAEngineLinked() {
		return "", nil, nil
	}
	if p := getenv(hoopInspectEnv); p != "" {
		if !exists(p) {
			return "", lanes, fmt.Errorf("%s=%s: no such file", hoopInspectEnv, p)
		}
		return p, lanes, nil
	}
	if p, err := lookPath(hoopInspectName); err == nil {
		return p, lanes, nil
	}
	for _, p := range hoopInspectPaths {
		if exists(p) {
			return p, lanes, nil
		}
	}
	return "", lanes, errors.New("this config has an ica lane and this binary carries no ICA engine; " +
		"hoop-inspect from the ica assets bundle serves it. Install the bundle " +
		"(https://releases.hoop.dev/release/<version>/hoop-ica-assets_<version>_Linux_<arch>.tar.gz, " +
		"see the ica section of sidecar/README.md) or set " + hoopInspectEnv + " to the binary")
}

// icaHandoffArgs is the hoop-inspect argv for this run's flags, argv[0]
// included. hoop-inspect takes the same flags under single dashes; the
// ones that mean nothing to it (the log format, --migrate, --codec-test)
// are handled before the handoff or draw its usage error.
func icaHandoffArgs(config, license, token string, validate, strict bool) []string {
	args := []string{hoopInspectName, "-config", config}
	if license != "" {
		args = append(args, "-license", license)
	}
	if token != "" {
		args = append(args, "-token", token)
	}
	if validate {
		args = append(args, "-validate")
	}
	if strict {
		args = append(args, "-strict")
	}
	return args
}
