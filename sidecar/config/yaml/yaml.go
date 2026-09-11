// Package yaml loads a hoop-inspect configuration written in YAML.
//
// # The separate module
//
// The sidecar root module carries libhoop and nothing else, and the stdlib has no
// YAML parser. Adding one to the root would put a dependency edge on every
// consumer of the library, including the ones that hand it JSON from a
// ConfigMap and have no use for another syntax. So it lives here, behind the
// same nested-module boundary as store/sqlite and pii/alcatraz.
//
// # Transcoding instead of yaml tags
//
// Every config struct already carries snake_case JSON tags (pattern_regex,
// keep_last, idle_timeout_sec) and YAML keys map onto them one for one. So
// this package parses YAML into a generic value, marshals that to JSON, and
// hands the bytes to daemon.LoadConfigBytes.
//
// The alternative is a second set of yaml tags on forty fields. That gives
// two schemas that drift, and it loses DisallowUnknownFields: yaml.v3's
// KnownFields does not see into the json.RawMessage sections (pii,
// mask.rules) that the detector plugin owns, so a typo inside them would be
// silently dropped. One decode path means one set of rules about what a
// valid config is.
//
// # Anchors
//
// The transcode preserves YAML anchors and aliases, because yaml.v3 resolves
// them during parsing. JSON cannot express them, and they are the reason to
// offer YAML at all: write a shared rule block once and reference it from
// several listeners.
//
//	x-readonly: &readonly
//	  - {name: no-writes, type: operation, operations: [insert, update, delete]}
//
//	listeners:
//	  - {name: replica-a, ..., policy: {rules: *readonly}}
//	  - {name: replica-b, ..., policy: {rules: *readonly}}
//
// Keys beginning "x-" are ignored, so an anchor block does not have to be a
// real config field.
package yaml

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoophq/hoop/sidecar/daemon"
	yamlv3 "gopkg.in/yaml.v3"
)

// AnchorPrefix names the key prefix reserved for YAML anchor blocks.
//
// A top-level "x-anything" key is dropped before validation, so you can park
// a shared rule list somewhere without inventing a config field for it.
// docker-compose uses the same convention for the same purpose.
const AnchorPrefix = "x-"

// Load reads a config file, choosing the parser by extension: .yaml and .yml
// are transcoded, anything else is read as JSON.
//
// A main should call this: one function accepting either syntax, with the
// file extension picking the parser.
func Load(path string) (*daemon.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if IsYAML(path) {
		return LoadYAMLBytes(data)
	}
	return daemon.LoadConfigBytes(data)
}

// IsYAML reports whether a path should be parsed as YAML.
func IsYAML(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return true
	}
	return false
}

// LoadYAMLBytes transcodes YAML to JSON and validates it.
//
// Errors from the JSON stage name JSON constructs, a small wart: a YAML
// author reading "unknown field" still gets the right field name, so the
// message survives the translation even though its syntax does not.
func LoadYAMLBytes(data []byte) (*daemon.Config, error) {
	jsonBytes, err := ToJSON(data)
	if err != nil {
		return nil, err
	}
	return daemon.LoadConfigBytes(jsonBytes)
}

// ToJSON converts YAML bytes to the equivalent JSON document.
//
// Exported so a caller can inspect the translation. "What did my YAML become"
// is the first question when a config misbehaves, and answering it should not
// require a debugger.
func ToJSON(data []byte) ([]byte, error) {
	var root any
	if err := yamlv3.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if root == nil {
		return nil, fmt.Errorf("parse yaml: empty document")
	}

	cleaned, err := normalize(root, "")
	if err != nil {
		return nil, err
	}
	if top, ok := cleaned.(map[string]any); ok {
		for k := range top {
			if strings.HasPrefix(k, AnchorPrefix) {
				delete(top, k)
			}
		}
	}

	out, err := json.Marshal(cleaned)
	if err != nil {
		return nil, fmt.Errorf("convert yaml to json: %w", err)
	}
	return out, nil
}

// FromJSON renders a JSON document as YAML, preserving the document's key
// order.
//
// It exists for -migrate, which rebuilds a config from the parsed struct
// and owes the operator a file in the syntax they wrote. Order matters for
// a file a person maintains, and yaml.Marshal of a decoded map would
// scramble it (Go maps carry no order), so this walks the JSON token
// stream and builds a yaml.Node tree instead: the encoder then emits keys
// exactly as json.Marshal ordered them, which is struct declaration order.
func FromJSON(data []byte) ([]byte, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	node, err := nodeFromJSON(dec)
	if err != nil {
		return nil, fmt.Errorf("convert json to yaml: %w", err)
	}
	var buf strings.Builder
	enc := yamlv3.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return nil, fmt.Errorf("convert json to yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("convert json to yaml: %w", err)
	}
	return []byte(buf.String()), nil
}

// nodeFromJSON consumes one JSON value from dec as a yaml.Node.
func nodeFromJSON(dec *json.Decoder) (*yamlv3.Node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return nodeFromToken(dec, tok)
}

// nodeFromToken builds the node a token opens: a container's children are
// consumed from the decoder, a scalar is complete in the token itself.
func nodeFromToken(dec *json.Decoder, tok json.Token) (*yamlv3.Node, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			node := &yamlv3.Node{Kind: yamlv3.MappingNode, Tag: "!!map"}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, fmt.Errorf("object key %v is not a string", keyTok)
				}
				keyNode := &yamlv3.Node{}
				keyNode.SetString(key)
				valNode, err := nodeFromJSON(dec)
				if err != nil {
					return nil, err
				}
				node.Content = append(node.Content, keyNode, valNode)
			}
			_, err := dec.Token() // consume '}'
			return node, err
		case '[':
			node := &yamlv3.Node{Kind: yamlv3.SequenceNode, Tag: "!!seq"}
			for dec.More() {
				child, err := nodeFromJSON(dec)
				if err != nil {
					return nil, err
				}
				node.Content = append(node.Content, child)
			}
			_, err := dec.Token() // consume ']'
			return node, err
		}
		return nil, fmt.Errorf("unexpected delimiter %v", t)
	case string:
		// SetString picks the safe style: plain where possible, quoted
		// where the value would otherwise read as a number or a bool,
		// literal for multiline. That is exactly the round-trip rule
		// ToJSON needs to hold in the other direction.
		node := &yamlv3.Node{}
		node.SetString(t)
		return node, nil
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(t.String(), ".eE") {
			tag = "!!float"
		}
		return &yamlv3.Node{Kind: yamlv3.ScalarNode, Tag: tag, Value: t.String()}, nil
	case bool:
		return &yamlv3.Node{Kind: yamlv3.ScalarNode, Tag: "!!bool", Value: fmt.Sprintf("%t", t)}, nil
	case nil:
		return &yamlv3.Node{Kind: yamlv3.ScalarNode, Tag: "!!null", Value: "null"}, nil
	}
	return nil, fmt.Errorf("unexpected token %v", tok)
}

// normalize rewrites a decoded YAML value into something encoding/json can
// marshal.
//
// yaml.v3 decodes mappings as map[string]any when every key is a string, and
// as map[any]any the moment one is not, which encoding/json rejects. A
// non-string key is a config mistake here regardless, since no field in the
// schema is keyed by a number, so this reports it with its path rather than
// coercing. "listeners[0].policy: mapping key 1 is not a string" is
// actionable; a stringified key is a field name nobody wrote.
func normalize(v any, path string) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			child, err := normalize(val, join(path, k))
			if err != nil {
				return nil, err
			}
			out[k] = child
		}
		return out, nil

	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			ks, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("%s: mapping key %v is not a string",
					pathOr(path, "config"), k)
			}
			child, err := normalize(val, join(path, ks))
			if err != nil {
				return nil, err
			}
			out[ks] = child
		}
		return out, nil

	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			child, err := normalize(val, fmt.Sprintf("%s[%d]", pathOr(path, ""), i))
			if err != nil {
				return nil, err
			}
			out[i] = child
		}
		return out, nil

	default:
		return v, nil
	}
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func pathOr(path, fallback string) string {
	if path == "" {
		return fallback
	}
	return path
}
