package services

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

	// The codec registry is what answers "is this a protocol we speak?", and
	// it is populated by side effect. Without this import inspect.New knows
	// nothing and ValidateStructure would refuse every listener. The two
	// binaries that serve these routes disagree on their own: the hoop CLI
	// links the codecs for "hoop start sidecar", gateway/cmd/gateway does
	// not, so leaving it to chance makes the same request answer differently
	// depending on how the control plane was started.
	_ "github.com/hoophq/hoop/sidecar/codec/all"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// GenerateSidecarKey returns the token a sidecar authenticates with. The
// "hsc_" prefix keeps it out of the "hpk_" branch of the API auth middleware.
func GenerateSidecarKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random key: %w", err)
	}
	return "hsc_" + base64.RawURLEncoding.EncodeToString(b), nil
}

// ParseSidecarConfiguration decodes a config document from the API into the
// daemon's own type, so the gateway stores a config it could serve rather than
// bytes nobody checked.
//
// The decode is strict, the same rule daemon.LoadConfigBytes follows, so a
// misspelled key is a rejected write instead of a sidecar running with a
// control silently disabled.
//
// An absent document (no bytes) is the zero config, which a sidecar refuses at
// startup for having no listeners. An explicit null is refused here instead:
// "configuration": null claims to name a document and names nothing, and the
// published schema declares an object.
//
// The listeners are then checked with daemon.ValidateStructure, which asks
// everything the document can answer on its own: the protocol is one this
// build speaks, both addresses are present, the transport is tcp or unix, no
// two lanes bind the same address, and downstream_tls sits on a lane that
// terminates it. The control plane authors these documents and never runs
// one, so a check skipped here is a typo that travels to the customer's host
// and surfaces there as a sidecar that refuses to boot.
//
// The full daemon.Validate is still NOT run: it loads the listener TLS
// keypairs from the local filesystem, which exists on the sidecar host and not
// on the gateway, and validateLane answers for a resolved stack whose analyzer
// providers are whatever that binary linked. ValidateStructure is the subset
// that holds for both, so nothing a sidecar would accept is refused here.
//
// A document with no listeners at all passes. A sidecar is created before it
// is configured, and a sidecar seeding the plane has its own "at least one
// listener" rule in ImportConfiguration.
func ParseSidecarConfiguration(raw json.RawMessage) (daemon.Config, error) {
	var cfg daemon.Config
	document := bytes.TrimSpace(raw)
	if len(document) == 0 {
		return cfg, nil
	}
	if bytes.Equal(document, []byte("null")) {
		return cfg, fmt.Errorf("invalid sidecar configuration: expected an object, got null")
	}
	dec := json.NewDecoder(bytes.NewReader(document))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("invalid sidecar configuration: %w", err)
	}
	// Deliberately not normalized first. Every deprecated spelling normalize
	// folds lives in a field ValidateStructure does not read, so the answer is
	// the same either way, and normalizing would rewrite the document the
	// caller asked us to store.
	if err := cfg.ValidateStructure(); err != nil {
		return cfg, fmt.Errorf("invalid sidecar configuration: %w", err)
	}
	return cfg, nil
}
