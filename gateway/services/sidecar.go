package services

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

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
// daemon.Validate is NOT run: it loads the listener TLS keypairs from the
// local filesystem, which exists on the sidecar host and not on the gateway.
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
	return cfg, nil
}
