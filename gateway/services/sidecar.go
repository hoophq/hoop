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
	// load_from_disk false is the same fact as absent: the control plane owns
	// the document. Drop it so the stored document never carries a key an
	// older sidecar rejects via DisallowUnknownFields on its next handshake.
	if cfg.LoadFromDisk != nil && !*cfg.LoadFromDisk {
		cfg.LoadFromDisk = nil
	}
	return cfg, nil
}

// ParseSidecarConfigurationPatch validates a partial configuration document and
// splits it into what a PATCH applies: the merge object, and whether the
// load_from_disk key must be dropped. Unknown keys are rejected the same way the
// full-document write rejects them, so a typo is a failed patch rather than a
// control silently dropped.
//
// The merge is presence-aware: only the keys the caller sent are returned, so
// the stored document keeps every field the patch does not name. load_from_disk
// is the one field with absent==false semantics: patching it false (or null)
// removes the key instead of storing a value an older sidecar rejects via
// DisallowUnknownFields, matching the canonicalization the full-document path
// performs.
func ParseSidecarConfigurationPatch(raw json.RawMessage) (merge json.RawMessage, removeLoadFromDisk bool, err error) {
	document := bytes.TrimSpace(raw)
	if len(document) == 0 {
		return json.RawMessage("{}"), false, nil
	}
	if bytes.Equal(document, []byte("null")) {
		return nil, false, fmt.Errorf("invalid sidecar configuration: expected an object, got null")
	}
	dec := json.NewDecoder(bytes.NewReader(document))
	dec.DisallowUnknownFields()
	var probe daemon.Config
	if err := dec.Decode(&probe); err != nil {
		return nil, false, fmt.Errorf("invalid sidecar configuration: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		return nil, false, fmt.Errorf("invalid sidecar configuration: %w", err)
	}
	if v, ok := fields["load_from_disk"]; ok {
		var on *bool
		if err := json.Unmarshal(v, &on); err != nil {
			return nil, false, fmt.Errorf("invalid sidecar configuration: %w", err)
		}
		if on == nil || !*on {
			delete(fields, "load_from_disk")
			removeLoadFromDisk = true
		}
	}
	// A license is never a sidecar key (see the create and PUT paths). Drop it
	// rather than reject, so a document round-tripped from GET still patches.
	delete(fields, "license")
	merged, err := json.Marshal(fields)
	if err != nil {
		return nil, false, err
	}
	return merged, removeLoadFromDisk, nil
}
