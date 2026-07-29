// Command hoop-inspect is an inspecting TCP relay: it decodes the wire
// protocol between a client and a database or API, evaluates each statement
// against policy, records an audit trail naming the human who ran it, and
// masks sensitive values on the way back.
//
// This build has ZERO dependencies. It compiles without a module download,
// which is what lets the container image be reproduced and audited without a
// supply-chain review. Masking uses the eight built-in detectors (email, SSN,
// credit card, phone, IP, AWS key, JWT, private key).
//
// For 45 entity types across 12 countries — checksum-verified national IDs,
// IBAN, and `pii` policy rules — build the sibling binary in the nested module
// github.com/hoophq/hoopinspect/pii/alcatraz, which is this same relay with a
// detector attached.
//
// Usage:
//
//	hoop-inspect -config /etc/hoop-inspect/config.json
//	hoop-inspect -validate -config config.json   # check and exit
//	hoop-inspect -version
package main

import "github.com/hoophq/hoopinspect/sidecar"

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() { sidecar.Main(version, nil) }
