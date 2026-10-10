package policy

import (
	"fmt"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// The protocol-agnostic rule type. Every other local type reads a field the
// Statement has a name for: text, operation, relations, HTTP detail. A codec
// for a protocol this module never heard of has none of those to offer beyond
// text and operation, and what it does know — the verb on its own wire, a
// flag the frame carried — goes in Statement.Metadata. This type lets a rule
// read that map, so a plug-in codec gets a policy surface without a rule type
// of its own in this package for every protocol it adds.
const (
	// MatchMetadata matches when the statement carries every key in
	// Metadata with one of that key's values. Keys are ANDed, values under
	// one key are ORed, and both sides are lowercased before an exact
	// comparison, so `X-Acmewire.Verb: [Purge]` reads the same as
	// `x-acmewire.verb: [purge]`. A key the statement lacks never matches:
	// a rule written for one protocol's keys stays silent on every other
	// lane in a mixed rule set.
	//
	// Plug-in codecs put their verb under `<protocol>.verb`, mirroring the
	// `grpc.*` and `mongodb.command` keys the built-in codecs use, so a
	// rule denying one verb reads `{<protocol>.verb: [<VERB>]}`.
	MatchMetadata MatchType = "metadata"
)

// validateMetadata checks the fields of a metadata rule at construction. A
// key with no values would match on presence alone; that is what http_header
// means by an empty list, but here a codec documents each key's values, so
// a rule naming none has most likely lost them to a YAML mistake, and it
// would fire on every statement of that protocol.
func (r Rule) validateMetadata() error {
	if len(r.Metadata) == 0 {
		return fmt.Errorf("%s: metadata rule with no keys", r.Name)
	}
	for key, values := range r.Metadata {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s: metadata rule with an empty key", r.Name)
		}
		if len(values) == 0 {
			return fmt.Errorf("%s: metadata rule names key %q with no values", r.Name, key)
		}
	}
	return nil
}

// matchesMetadata evaluates the metadata rule type. ok reports whether this
// rule type belongs here, mirroring matchesHTTP and matchesGRPC.
func (r Rule) matchesMetadata(stmt inspect.Statement) (matched, ok bool) {
	if r.Type != MatchMetadata {
		return false, false
	}
	for key, want := range r.Metadata {
		got, present := metadataValue(stmt.Metadata, key)
		if !present {
			return false, true
		}
		got = strings.ToLower(got)
		matchedOne := false
		for _, v := range want {
			if strings.ToLower(v) == got {
				matchedOne = true
				break
			}
		}
		if !matchedOne {
			return false, true
		}
	}
	return true, true
}

// metadataValue looks key up in metadata without regard to case. Codecs
// write their keys lowercased by convention, but a rule is typed by hand,
// and a map lookup cannot fold case, so this walks the map; a statement
// carries a handful of keys and the walk is cheaper than normalizing a
// copy per statement.
func metadataValue(metadata map[string]string, key string) (string, bool) {
	key = strings.TrimSpace(key)
	if v, present := metadata[key]; present {
		return v, true
	}
	for k, v := range metadata {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}
