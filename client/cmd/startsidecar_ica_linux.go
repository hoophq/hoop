//go:build linux

package cmd

// The ICA engine a `protocol: ica` lane needs, same rule as the providers in
// startsidecar.go: turning on Citrix inspection must not require a different
// binary. Linux only, because that is where the lane runs: CIDR mode needs
// IP_TRANSPARENT and CAP_NET_ADMIN, and the OCR worker the engine spawns is
// a Linux build. The darwin and windows CLIs stay static, cross-compile, and
// refuse an ica lane at startup.
//
// The engine's NER runs on ONNX Runtime, a cgo backend, so the Linux `hoop`
// is a `CGO_ENABLED=1 -tags ORT` build (Makefile build-go). At run time it
// reads the worker, tessdata, libonnxruntime and the model from the paths
// deploy/ica-sidecar/install.sh lays out; a host without them still runs
// every other lane, and sidecar/ica tells the daemon not to advertise
// protocol:ica to a control plane there (daemon.RegisterICAEngine).
import _ "github.com/hoophq/hoop/sidecar/ica"
