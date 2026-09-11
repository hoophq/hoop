package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/hoophq/hoop/sidecar/policy"
)

// This file is the -migrate command: it rewrites a config that uses
// deprecated spellings onto the canonical schema and emits the result.
//
// Most of the migration is not here. LoadConfigBytes already folds every
// renamed field (policy -> guardrails/opa, connection -> name, mask.enabled,
// audit.fail_closed) through normalize, so by the time a Config reaches this
// file those fields are canonical and only the one deprecation normalize
// cannot fold remains: `type: ai_analysis` rules, whose faithful home is a
// listener's analyzer block. normalize does not fold them because the fold
// is not always faithful — two rules on one lane cannot become one block —
// so it lives here, where a partial move can be reported instead of guessed.

// YAMLFromJSON renders a JSON document as YAML, for -migrate output.
//
// A package variable rather than a parameter for the same reason Loader is
// injected: the root module carries no YAML dependency, so a main that
// links github.com/hoophq/hoop/sidecar/config/yaml assigns its FromJSON
// here. Nil means -migrate emits JSON whatever the file extension says.
var YAMLFromJSON func(jsonDoc []byte) ([]byte, error)

// MigrateDeprecated rewrites c in place onto the canonical schema and
// returns operator-facing notes describing what moved and what could not.
//
// It expects a NORMALIZED config (anything from a Loader or LoadConfigBytes
// qualifies): the renamed fields are already folded, and only ai_analysis
// rules remain. The move is attempted only where it is faithful:
//
//   - A listener's own ai_analysis rule becomes the listener's analyzer
//     block when the lane has exactly one and no block yet.
//   - A single TOP-LEVEL ai_analysis rule reached every lane through rule
//     concatenation, so it becomes a block on every listener — but only
//     when every listener is free to take it, because a lane that already
//     has a block cannot absorb a second trigger/action map.
//
// Everything else stays as rules, keeps working, and is named in the notes
// with the reason, so the operator finishes by hand instead of the tool
// guessing.
func (c *Config) MigrateDeprecated() []string {
	var notes []string
	note := func(format string, args ...any) {
		notes = append(notes, fmt.Sprintf(format, args...))
	}

	// Listener-scope rules first: the lane's own spelling has the
	// strongest claim on the lane's block.
	for i := range c.Listeners {
		lc := &c.Listeners[i]
		if lc.Guardrails == nil {
			continue
		}
		local, ai := splitAnalyzerRules(lc.Guardrails.Rules)
		if len(ai) == 0 {
			continue
		}
		name := lc.displayName(i)
		switch {
		case lc.Analyzer != nil:
			note("%s: %d ai_analysis rule(s) left in place: the listener already has "+
				"an analyzer block, and a lane runs one block", name, len(ai))
		case len(ai) > 1:
			note("%s: %d ai_analysis rules left in place: they carry separate triggers, "+
				"actions or prompts, and one block cannot hold two; merge them or keep "+
				"the rule form until you can", name, len(ai))
		default:
			la := specFromRule(ai[0])
			lc.Analyzer = &la
			lc.Guardrails.Rules = local
			if len(local) == 0 && lc.Guardrails.Mode == "" {
				lc.Guardrails = nil
			}
			note("%s: rule %q became the listener's analyzer block. Its audit ai_rule "+
				"and its max_calls budget now key on the listener name %q instead of "+
				"the rule name", name, ai[0].Name, name)
		}
	}

	// A top-level rule reached every lane. It moves only when every lane
	// can take it, because a partial move would change which lanes run it.
	if c.Guardrails == nil {
		return notes
	}
	local, ai := splitAnalyzerRules(c.Guardrails.Rules)
	switch {
	case len(ai) == 0:
	case len(ai) > 1:
		note("guardrails: %d top-level ai_analysis rules left in place: each lane "+
			"runs one analyzer block, and one lane cannot take %d", len(ai), len(ai))
	default:
		occupied := make([]string, 0, len(c.Listeners))
		for i := range c.Listeners {
			lc := &c.Listeners[i]
			if lc.Analyzer != nil || hasAIRules(lc.Guardrails) {
				occupied = append(occupied, lc.displayName(i))
			}
		}
		if len(occupied) > 0 {
			note("guardrails: top-level ai_analysis rule %q left in place: %s already "+
				"carry an analyzer, and moving it to the free lanes only would change "+
				"which lanes run it", ai[0].Name, strings.Join(occupied, ", "))
			break
		}
		for i := range c.Listeners {
			la := specFromRule(ai[0])
			c.Listeners[i].Analyzer = &la
		}
		c.Guardrails.Rules = local
		if len(local) == 0 && c.Guardrails.Mode == "" {
			c.Guardrails = nil
		}
		note("guardrails: top-level rule %q became an analyzer block on every "+
			"listener. Each lane now pays from its own max_calls budget, keyed on "+
			"the listener name, where the shared rule paid from one purse named %q",
			ai[0].Name, ai[0].Name)
	}
	return notes
}

// hasAIRules reports whether a guardrails block still authors ai_analysis
// rules after the listener-scope pass.
func hasAIRules(gc *GuardrailsConfig) bool {
	if gc == nil {
		return false
	}
	for _, r := range gc.Rules {
		if r.Type == policy.MatchAIAnalysis {
			return true
		}
	}
	return false
}

// RenderMigrated marshals a migrated config as a JSON document fit to be a
// config file.
//
// A plain json.Marshal of Config is correct but noisy: fields with no
// omitempty render their zero values ("network": "", "upstream_tls": null),
// which an operator would have to clean by hand. So the render prunes
// zero-ish values from the document and then PROVES the prune changed
// nothing: the pruned document is loaded through LoadConfigBytes and
// re-marshaled, and only a byte-identical result ships. A prune that moved
// semantics — an explicit `fail_open: false` pointer, an `opa: {}` opt-out —
// fails that comparison and the noisy-but-exact document ships instead.
//
// The prune walks an ORDER-PRESERVING decode of the document rather than a
// Go map, because the output is a file a person maintains: json.Marshal
// ordered the keys by struct declaration — the order the docs teach — and
// a map round-trip would alphabetize them.
func RenderMigrated(c *Config) ([]byte, error) {
	exact, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(exact))
	dec.UseNumber()
	doc, err := decodeOrdered(dec)
	if err != nil {
		return nil, err
	}
	pruned, err := json.MarshalIndent(prune(doc, "", ""), "", "  ")
	if err != nil {
		return nil, err
	}

	reloaded, err := LoadConfigBytes(pruned)
	if err == nil {
		remarshal, merr := json.Marshal(reloaded)
		if merr == nil && bytes.Equal(remarshal, exact) {
			return append(pruned, '\n'), nil
		}
	}
	// The prune was not provably lossless for this config; ship the exact
	// document. Indented so it is still a file a person maintains.
	exact, err = json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(exact, '\n'), nil
}

// prune drops zero values a strict decode reads back identically: null,
// empty strings, zero numbers, false, and empty containers. The exceptions
// are the spellings where presence itself is the meaning:
//
//   - `opa`, `guardrails`, `mask` and `analyzer` objects stay even empty,
//     because an empty block can be an opt-out (`opa: {}`) or an explicit
//     scope an operator wrote.
//   - `rules: []` stays, because an empty list is how a lane switches an
//     inherited rule set off.
//   - `fail_open: false` stays under `analyzer` and `audit` blocks, where
//     the field is a pointer and explicit false is not the same marshal as
//     absent. Under `opa` it is a plain bool whose zero is absence, so it
//     drops like any other zero.
//
// RenderMigrated verifies the result round-trips byte-identically, so a
// miss here degrades output cosmetics, never semantics.
func prune(v any, key, parent string) any {
	switch t := v.(type) {
	case *orderedMap:
		out := &orderedMap{}
		for _, kv := range t.pairs {
			p := prune(kv.val, kv.key, key)
			if p == nil && !keepEmpty(kv.key, kv.val) {
				continue
			}
			if p == nil {
				p = kv.val
			}
			out.pairs = append(out.pairs, pair{kv.key, p})
		}
		if len(out.pairs) == 0 && !keepEmpty(key, t) {
			return nil
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, child := range t {
			p := prune(child, "", key)
			if p == nil {
				continue
			}
			out = append(out, p)
		}
		if len(out) == 0 && !keepEmpty(key, t) {
			return nil
		}
		return out
	case string:
		if t == "" {
			return nil
		}
	case json.Number:
		if f, err := t.Float64(); err == nil && f == 0 {
			return nil
		}
	case bool:
		if !t && !(key == "fail_open" && (parent == "analyzer" || parent == "audit")) {
			return nil
		}
	case nil:
		return nil
	}
	return v
}

// keepEmpty names the keys whose empty value is a meaning, not noise.
func keepEmpty(key string, v any) bool {
	switch key {
	case "opa", "guardrails", "mask", "analyzer":
		_, isMap := v.(*orderedMap)
		return isMap
	case "rules":
		_, isList := v.([]any)
		return isList
	}
	return false
}

// orderedMap is a JSON object that remembers its key order, so the pruned
// document keeps the order json.Marshal gave it. The stdlib map would
// alphabetize on re-marshal.
type orderedMap struct {
	pairs []pair
}

type pair struct {
	key string
	val any
}

// MarshalJSON renders the object with its remembered order.
// json.MarshalIndent re-indents this output, so the render stays readable.
func (m *orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, kv := range m.pairs {
		if i > 0 {
			buf.WriteByte(',')
		}
		k, err := json.Marshal(kv.key)
		if err != nil {
			return nil, err
		}
		buf.Write(k)
		buf.WriteByte(':')
		v, err := json.Marshal(kv.val)
		if err != nil {
			return nil, err
		}
		buf.Write(v)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// decodeOrdered consumes one JSON value from dec, decoding objects into
// orderedMaps and everything else into the stdlib shapes.
func decodeOrdered(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return orderedFromToken(dec, tok)
}

func orderedFromToken(dec *json.Decoder, tok json.Token) (any, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := &orderedMap{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, fmt.Errorf("object key %v is not a string", keyTok)
				}
				val, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				m.pairs = append(m.pairs, pair{key, val})
			}
			_, err := dec.Token() // consume '}'
			return m, err
		case '[':
			var list []any
			for dec.More() {
				child, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				list = append(list, child)
			}
			_, err := dec.Token() // consume ']'
			if list == nil {
				list = []any{}
			}
			return list, err
		}
		return nil, fmt.Errorf("unexpected delimiter %v", t)
	default:
		// string, json.Number, bool or nil, all complete in the token.
		return tok, nil
	}
}

// WriteMigrated runs the migration and writes the document to w and the
// report to errw. Shared by hoop-inspect -migrate and
// `hoop start sidecar --migrate`, so the two front ends cannot drift.
//
// yamlOut asks for YAML; it is honored only when the main injected
// YAMLFromJSON, and falls back to JSON with a line on the report otherwise.
// Comments and key order from the original file are not preserved — the
// document is rebuilt from the parsed config — which the report says out
// loud because an operator diffing the two files will notice first.
func WriteMigrated(cfg *Config, yamlOut bool, w, errw io.Writer) error {
	notes := cfg.MigrateDeprecated()

	jsonDoc, err := RenderMigrated(cfg)
	if err != nil {
		return fmt.Errorf("rendering the migrated config: %w", err)
	}

	// Prove the emitted document loads before anyone deploys it, and count
	// what it still warns about. JSON is what LoadConfigBytes reads; a YAML
	// render comes from this verified JSON, so verifying it covers both.
	reloaded, err := LoadConfigBytes(jsonDoc)
	if err != nil {
		return fmt.Errorf("the migrated config does not load, which is a bug worth "+
			"reporting: %w", err)
	}
	if n := len(reloaded.Deprecations); n > 0 {
		notes = append(notes, fmt.Sprintf(
			"the migrated config still uses %d deprecated field(s); the notes above "+
				"say which lanes to finish by hand", n))
	}

	doc := jsonDoc
	if yamlOut {
		if YAMLFromJSON == nil {
			fmt.Fprintln(errw, "migrate: this build renders JSON only; the document below is JSON")
		} else {
			doc, err = YAMLFromJSON(jsonDoc)
			if err != nil {
				return fmt.Errorf("rendering the migrated config as YAML: %w", err)
			}
		}
	}

	for _, n := range notes {
		fmt.Fprintln(errw, "migrate:", n)
	}
	fmt.Fprintln(errw, "migrate: comments and key order from the original file are not preserved; "+
		"review the diff before deploying")

	_, err = w.Write(doc)
	return err
}

// isYAMLPath reports whether a path's extension asks for YAML. It mirrors
// config/yaml.IsYAML without the import: the root module cannot depend on
// the nested one, and two lines do not earn an interface.
func isYAMLPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return true
	}
	return false
}
