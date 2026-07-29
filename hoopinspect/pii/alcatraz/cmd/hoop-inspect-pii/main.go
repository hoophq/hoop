// Command hoop-inspect-pii is the hoop-inspect relay: an inspecting TCP proxy
// that decodes the wire protocol between a client and a database or API,
// evaluates each statement against policy, records an audit trail naming the
// human who ran it, and masks sensitive values on the way back.
//
// It runs behind something that already owns TLS and identity — Envoy,
// typically, forwarding plaintext over loopback or a unix socket. It is not a
// router: one listener, one upstream, one protocol per endpoint.
//
// # Why the binary lives here
//
// The relay itself is assembled by github.com/hoophq/hoopinspect/sidecar, in
// the dependency-free root module. This main sits in the nested pii/alcatraz
// module because it wires in alcatraz PII detection, and a main in the root
// could not import that without putting the dependency in the root's go.mod.
//
// The plugin supplies both halves of detection: 45 alcatraz entity types
// across 12 countries (25 checksum-verified) plus three credential
// recognizers, driving
//
//   - response masking, where a rule names an entity and a strategy;
//   - policy rules of type "pii", which deny a statement that embeds a
//     national identifier — something no amount of response masking undoes
//     once the query is in the database's own log.
//
// Omit the "pii" config section and the relay runs with detection disabled:
// masking is then unavailable and a pii rule is a config error, both refused
// at startup rather than silently skipped.
//
// Configure the detector under "pii" in the same config file:
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
// pii.entities is required: there is no all-entities default, because turning
// on all 45 recognizers rewrites ordinary numeric columns as US_SSN. See the
// package documentation for the measured rates.
//
// Usage:
//
//	hoop-inspect-pii -config /etc/hoop-inspect/config.json
//	hoop-inspect-pii -validate -config config.json
//	hoop-inspect-pii -version
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	configyaml "github.com/hoophq/hoopinspect/config/yaml"
	"github.com/hoophq/hoopinspect/pii/alcatraz"
	"github.com/hoophq/hoopinspect/sidecar"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

// piiFile is the "pii" section of the shared config file. It is read here
// rather than in the sidecar package because the sidecar must not know what an
// alcatraz Options looks like — that is the whole point of the split.
type piiFile struct {
	PII *struct {
		Entities  []string `json:"entities"`
		Ignored   []string `json:"ignored,omitempty"`
		Threshold float64  `json:"threshold,omitempty"`
		AllowList []string `json:"allow_list,omitempty"`
		Language  string   `json:"language,omitempty"`
	} `json:"pii"`
}

func main() {
	// The config path is parsed twice: once here for the "pii" section, once
	// by sidecar.Main for everything else. Peeking rather than taking over
	// the flag set keeps one owner of the CLI contract.
	cfgPath := peekConfigPath()

	det, err := buildDetector(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hoop-inspect-pii:", err)
		os.Exit(1)
	}
	// A nil *alcatraz.Detector in a non-nil interface would make the sidecar
	// think a detector is present and call through it. Pass an untyped nil.
	if det == nil {
		sidecar.Main(version, nil, configyaml.Load)
		return
	}
	sidecar.Main(version, det, configyaml.Load)
}

// peekConfigPath finds -config without consuming the flag set, so
// sidecar.Main still owns flag parsing, -help and the error messages.
func peekConfigPath() string {
	fs := flag.NewFlagSet("peek", flag.ContinueOnError)
	fs.SetOutput(os.NewFile(0, os.DevNull))
	path := fs.String("config", "", "")
	fs.Bool("validate", false, "")
	fs.Bool("version", false, "")
	_ = fs.Parse(os.Args[1:])
	return *path
}

// buildDetector reads the "pii" section and constructs the detector. A missing
// section means no detector: this binary then behaves exactly like the base
// one, which is the right answer for a config written before the section
// existed.
func buildDetector(cfgPath string) (*alcatraz.Detector, error) {
	if cfgPath == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		// Let sidecar.Main report an unreadable config; it owns that message.
		return nil, nil
	}

	// The sidecar accepts YAML or JSON, so this second read of the same file
	// has to agree about the syntax. Transcoding here rather than parsing
	// YAML directly keeps one definition of what the "pii" section looks
	// like — the JSON tags below.
	if configyaml.IsYAML(cfgPath) {
		converted, cerr := configyaml.ToJSON(raw)
		if cerr != nil {
			// A malformed config is the sidecar's error to report.
			return nil, nil
		}
		raw = converted
	}

	var f piiFile
	if err := json.Unmarshal(raw, &f); err != nil {
		// Same: a malformed config is the sidecar's error to report.
		return nil, nil
	}
	if f.PII == nil {
		return nil, nil
	}

	det, err := alcatraz.NewDetector(alcatraz.Options{
		Entities:  f.PII.Entities,
		Ignored:   f.PII.Ignored,
		Threshold: f.PII.Threshold,
		AllowList: f.PII.AllowList,
		Language:  f.PII.Language,
	})
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", cfgPath, err)
	}
	return det, nil
}
