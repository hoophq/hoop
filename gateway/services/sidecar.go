package services

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/license"
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
	if err := ValidateListenerNames(cfg.Listeners); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// ValidateListenerNames refuses a document whose listeners cannot be addressed
// by name.
//
// The daemon does not require this. It keys listener uniqueness on the bind
// address (`network|listen`, sidecar/daemon/config.go), lets a name be absent
// and falls back to the positional label `listener[i]`
// (sidecar/daemon/daemon.go displayName). Two lanes may therefore legitimately
// share a name in a config file, and one may have no name at all.
//
// A control-plane document cannot afford either. The name is the only handle
// anything outside the document has on a lane: an approval rule is authorized
// through it (gateway/api/sidecar/reviews.go listenerNamesApprovalRule, which
// refuses when a name matches more than once), and the reload path keys its
// per-lane rule documents, its previous lanes and its running servers by it
// (sidecar/daemon/reload.go). A duplicate name silently re-points whatever
// named it, and a missing name cannot be named at all.
//
// This is enforced here and NOT in daemon.Validate on purpose. Tightening the
// daemon would refuse a standalone config that boots today, which is the
// upgrade break ADR-0011 rejected as its option 1. The control-plane document
// has no such installed base: every listener in every config under deploy/
// already carries a distinct name.
//
// The refusal names the listener by position, because a document with no names
// has nothing else to call it by.
func ValidateListenerNames(listeners []daemon.ListenerConfig) error {
	seen := make(map[string]int, len(listeners))
	for i, l := range listeners {
		name := strings.TrimSpace(l.Name)
		if name == "" {
			return fmt.Errorf("invalid sidecar configuration: listeners[%d] has no name; "+
				"a name is how a rule, an approval rule and an audit row address this listener", i)
		}
		if first, dup := seen[name]; dup {
			return fmt.Errorf("invalid sidecar configuration: listeners[%d] and listeners[%d] "+
				"are both named %q; a listener name must be unique so that what names it "+
				"reaches exactly one listener", first, i, name)
		}
		seen[name] = i
	}
	return nil
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
	// Checked only when the patch names listeners: a patch that does not send
	// the key leaves the stored list untouched, and probe.Listeners would be
	// empty for it either way.
	if _, ok := fields["listeners"]; ok {
		if err := ValidateListenerNames(probe.Listeners); err != nil {
			return nil, false, err
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

// ErrSidecarConfigInvalid is a configuration the sidecar would refuse to load.
// The message and the problems are the sidecar's own.
type ErrSidecarConfigInvalid struct {
	Err      error
	Problems []string
}

func (e ErrSidecarConfigInvalid) Error() string { return e.Err.Error() }

// CheckSidecarConfiguration refuses a configuration the sidecar would refuse,
// with the daemon's own validation. The files it names live on the sidecar's
// host, so only the sidecar checks those. A load_from_disk document is not
// served, so it is not checked.
func CheckSidecarConfiguration(cfg daemon.Config) error {
	if cfg.LoadFromDisk != nil && *cfg.LoadFromDisk {
		return nil
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := daemon.CheckConfigBytes(raw); err != nil {
		invalid := ErrSidecarConfigInvalid{Err: err, Problems: []string{err.Error()}}
		var problems daemon.ConfigProblems
		if errors.As(err, &problems) {
			invalid.Problems = problems
		}
		return invalid
	}
	return nil
}

// ErrSidecarConfigOverCap is returned when a configuration authors more rules
// than the organization's license allows. The message carries the daemon's own
// per-site breakdown, so an admin reads which blocks to merge rather than a
// total.
type ErrSidecarConfigOverCap struct{ Problems []string }

func (e ErrSidecarConfigOverCap) Error() string {
	return strings.Join(e.Problems, "; ")
}

// CheckSidecarConfigurationLimits refuses a configuration the sidecar it is
// written for would refuse.
//
// The caps are per process and count what a document AUTHORS, across the
// top-level blocks and every listener (sidecar/daemon/limits.go). Unlicensed
// that is one guardrail rule and one data masking rule.
//
// This runs on the WRITE, not only when the document is served, because a
// sidecar refuses over-cap rules in two different ways and neither is visible
// from the control plane. A running process answers reloadRefused and keeps
// its OLD rules while still reporting itself recently seen; a starting one
// exits. So an over-cap document looks healthy until the fleet reschedules,
// and then every pod crash-loops at once. Refusing the save turns that into a
// 422 the admin reads while they are still looking at the form.
//
// The license is the organization's, resolved through license.Load so the
// signature is checked here rather than taken on trust: daemon caps move only
// for a verdict license.Load reached. An organization with no license document
// gets the zero Status, which is the free tier.
func CheckSidecarConfigurationLimits(cfg daemon.Config, licenseData json.RawMessage) error {
	lic := license.Status{}
	if len(bytes.TrimSpace(licenseData)) > 0 {
		lic = license.Load(license.Ref{
			Value:  string(licenseData),
			Source: "the organization license",
		})
	}
	if problems := cfg.CheckLimits(lic); len(problems) > 0 {
		return ErrSidecarConfigOverCap{Problems: problems}
	}
	return nil
}
