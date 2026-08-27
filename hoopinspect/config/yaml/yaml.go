// Package yaml loads a hoop-inspect configuration written in YAML.
//
// # The separate module
//
// The hoopinspect root module carries libhoop and nothing else, and the stdlib has no
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
// hands the bytes to sidecar.LoadConfigBytes.
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

	"github.com/hoophq/hoop/hoopinspect/sidecar"
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
func Load(path string) (*sidecar.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	if IsYAML(path) {
		return LoadYAMLBytes(data)
	}
	return sidecar.LoadConfigBytes(data)
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
func LoadYAMLBytes(data []byte) (*sidecar.Config, error) {
	jsonBytes, err := ToJSON(data)
	if err != nil {
		return nil, err
	}
	return sidecar.LoadConfigBytes(jsonBytes)
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
