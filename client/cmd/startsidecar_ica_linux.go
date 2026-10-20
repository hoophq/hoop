//go:build linux

package cmd

// The ICA engine a `protocol: ica` lane needs, same rule as the providers in
// startsidecar.go: validating or trying a Citrix lane must not require a
// different binary. Linux only, because that is where the lane runs: CIDR
// mode needs IP_TRANSPARENT and CAP_NET_ADMIN, and the OCR worker the engine
// spawns is a Linux build. The darwin and windows CLIs have no lane to spend
// the engine and its inference backend on.
//
// This binary has no cgo, so the engine's NER runs on hugot's Go backend.
// That is enough for --validate and for testing a lane; production serves
// sessions from hoop-inspect built with -tags ORT (the hoopsidecar -ica
// image and the ica assets bundle, deploy/ica-sidecar/), whose NER runs on
// ONNX Runtime. Both read the same model files at the same paths.
import _ "github.com/hoophq/hoop/sidecar/ica"
