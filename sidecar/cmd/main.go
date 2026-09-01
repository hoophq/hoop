// Command hoop-inspect is the hoop-inspect relay: an inspecting TCP proxy
// that decodes the wire protocol between a client and a database or API,
// evaluates each statement against policy, records an audit trail naming the
// human who ran it, and masks sensitive values on the way back.
//
// It runs behind something that already owns TLS and identity, typically
// Envoy forwarding plaintext over loopback or a unix socket. It routes
// nothing: one listener, one upstream, one protocol per endpoint.
//
// # The binary's home
//
// The relay itself is assembled by github.com/hoophq/hoop/sidecar/daemon, in
// the root module, whose only dependency is libhoop. This main sits in its own nested module
// because it links the optional plugins -- alcatraz PII detection and the
// YAML config front end -- and a main in the root could not import those
// without putting their dependencies in the root's go.mod.
//
// The same relay is reachable as `hoop start sidecar`, which links the same
// two plugins into the hoop CLI. Prefer this binary for a sidecar container
// that should carry nothing else.
//
// # What the config turns on
//
// There is one binary and no build tags. Every capability below is decided by
// the config file, so an operator adding a "pii" section does not also have to
// swap the binary out:
//
//   - "pii" absent: the detector covers all 54 entity types it knows, and
//     costs nothing until a rule asks it for one. A masker scans only for the
//     entities its own rules name, and a "pii" guardrail intersects the scan
//     with the entities its own rule names.
//   - "pii" present: the section NARROWS that set. Name entities to restrict
//     detection to them, or use "ignored" to drop the recognizers that fire on
//     ordinary business data. See the pii/alcatraz package documentation for
//     the measured rates.
//
// The config file may be YAML or JSON; the extension picks the parser.
//
//	{
//	  "pii": {
//	    "entities": ["BR_CPF", "IBAN_CODE", "CREDIT_CARD"],
//	    "allow_list": ["4111111111111111"]
//	  },
//	  "mask":       {"rules": [{"entities": ["BR_CPF"], "strategy": "redact"}]},
//	  "guardrails": {"mode": "enforce", "rules": [
//	    {"name": "no-cpf-in-query", "type": "pii", "entities": ["BR_CPF"],
//	     "message": "do not put a national ID in a query"}
//	  ]}
//	}
//
// # The pre-ADR-0011 spelling
//
// "policy" used to carry both Hoop's own rules and the OPA client. It split
// into "guardrails" and "opa", "mask.enabled" and "listeners[].connection"
// were dropped, and "mask.rules[].entity" became a list named "entities". Both
// spellings load; the old one prints a warning naming its replacement, and
// -strict turns that warning into a non-zero exit.
//
// Usage:
//
//	hoop-inspect -config /etc/hoop-inspect/config.yaml
//	hoop-inspect -validate -config config.yaml
//	hoop-inspect -validate -strict -config config.yaml
//	hoop-inspect -version
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	// Analyzer providers register themselves on import. Linking all three
	// keeps the "one binary, the config decides" rule the package doc
	// states: an operator turning on Vertex does not also have to swap the
	// binary. Only vertex costs a dependency, and it is confined to its own
	// module so the root does not carry it.
	_ "github.com/hoophq/hoop/sidecar/analyzer/anthropic"
	_ "github.com/hoophq/hoop/sidecar/analyzer/openai"
	_ "github.com/hoophq/hoop/sidecar/analyzer/vertex"
	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/pii/alcatraz"
)

// version is the release this binary reports at -version and on the admin
// /stats endpoint. A build overrides it with -ldflags "-X main.version=...";
// the shipped image stamps the same value from the Dockerfile.
var version = "0.1.0"

func main() {
	err := daemon.Main(version, configyaml.Load, func(raw json.RawMessage) (daemon.Plugin, error) {
		// An absent "pii" section no longer means no detector: the plugin
		// builds one over every entity type it knows and the section
		// narrows it. A nil Plugin now means only that a build linked no
		// detector at all, which this one does not.
		//
		// The conversion still matters. A nil alcatraz.Plugin converts to a
		// nil daemon.Plugin rather than to a non-nil interface holding a nil
		// pointer, which the sidecar would call through.
		return alcatraz.PluginFromConfig(raw)
	})
	if err == nil {
		return
	}
	// The one exit in the program. daemon.Main returns instead of exiting so
	// the format of the message and the code live in one place.
	fmt.Fprintln(os.Stderr, "hoop-inspect:", err)
	if errors.Is(err, daemon.ErrUsage) {
		os.Exit(2)
	}
	os.Exit(1)
}
