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
//   - "pii" absent: detection is off. Masking is unavailable and a policy rule
//     of type "pii" is a config error, both refused at startup rather than
//     silently skipped.
//   - "pii" present: 45 alcatraz entity types across 12 countries (25
//     checksum-verified) plus three credential recognizers drive response
//     masking, where a rule names an entity and a strategy, and policy rules
//     of type "pii", which deny a statement that embeds a national identifier.
//     No amount of response masking undoes that once the query is in the
//     database's own log.
//
// The config file may be YAML or JSON; the extension picks the parser.
//
//	{
//	  "pii": {
//	    "entities": ["BR_CPF", "IBAN_CODE", "CREDIT_CARD"],
//	    "allow_list": ["4111111111111111"]
//	  },
//	  "mask":   {"enabled": true, "rules": [{"entity": "BR_CPF", "strategy": "redact"}]},
//	  "policy": {"enforce": true, "rules": [
//	    {"name": "no-cpf-in-query", "type": "pii", "entities": ["BR_CPF"],
//	     "message": "do not put a national ID in a query"}
//	  ]}
//	}
//
// pii.entities is required whenever the section is present. There is no
// all-entities default, because turning on all 45 recognizers rewrites
// ordinary numeric columns as US_SSN. See the pii/alcatraz package
// documentation for the measured rates.
//
// Usage:
//
//	hoop-inspect -config /etc/hoop-inspect/config.yaml
//	hoop-inspect -validate -config config.yaml
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
		// A nil alcatraz.Plugin converts to a nil daemon.Plugin, so "no pii
		// section" stays nil rather than becoming a non-nil interface holding
		// a nil pointer, which the sidecar would call through.
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
