package yaml

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/daemon"
	yamlv3 "gopkg.in/yaml.v3"
)

// StarterFile is the name the first-run screen offers for the config it
// writes.
const StarterFile = "hoop-sidecar.yaml"

// DemoAPIKey marks a config whose upstream is the hoop CLI's built-in demo
// API: the CLI serves it at the key's address while the sidecar runs. The
// x- prefix keeps it out of daemon.Config (Load drops it), so a sidecar
// that is not the CLI never sees it.
const DemoAPIKey = AnchorPrefix + "hoop-demo-api"

// RenderOptions are the comments and extension keys Render adds around a
// config. The config itself is never written by hand: it is the struct,
// marshalled, so the file cannot drift from the schema.
type RenderOptions struct {
	// Header is the comment block above the document, one line per entry.
	Header []string
	// Comments is the comment block above a top-level key.
	Comments map[string][]string
	// Extensions are x- keys written first in the document.
	Extensions []Extension
	// Footer is the comment block below the document.
	Footer []string
}

// Extension is one top-level x- key and its string value.
type Extension struct{ Key, Value string }

// Render writes cfg as commented YAML, keys in struct order, the way
// -migrate writes a config. Comments ride on the YAML nodes, so they land
// beside the key they explain whatever the config holds.
func Render(cfg *daemon.Config, o RenderOptions) ([]byte, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("render config: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	root, err := nodeFromJSON(dec)
	if err != nil {
		return nil, fmt.Errorf("render config: %w", err)
	}
	if root.Kind != yamlv3.MappingNode {
		return nil, fmt.Errorf("render config: not a mapping")
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if c := o.Comments[root.Content[i].Value]; len(c) > 0 {
			root.Content[i].HeadComment = commentBlock(c)
		}
	}
	var ext []*yamlv3.Node
	for _, e := range o.Extensions {
		if !strings.HasPrefix(e.Key, AnchorPrefix) {
			return nil, fmt.Errorf("render config: extension key %q does not start with %q", e.Key, AnchorPrefix)
		}
		ext = append(ext,
			&yamlv3.Node{Kind: yamlv3.ScalarNode, Tag: "!!str", Value: e.Key},
			&yamlv3.Node{Kind: yamlv3.ScalarNode, Tag: "!!str", Value: e.Value})
	}
	root.Content = append(ext, root.Content...)

	doc := &yamlv3.Node{Kind: yamlv3.DocumentNode, Content: []*yamlv3.Node{root},
		HeadComment: commentBlock(o.Header), FootComment: commentBlock(o.Footer)}
	var buf strings.Builder
	enc := yamlv3.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("render config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("render config: %w", err)
	}
	return []byte(buf.String()), nil
}

// commentBlock turns lines into a yaml.v3 comment. Each line carries its own
// "#", so an empty entry is a bare "#" that keeps paragraphs apart.
func commentBlock(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		if l == "" {
			out[i] = "#"
			continue
		}
		out[i] = "# " + l
	}
	return strings.Join(out, "\n")
}

// ExtensionValue reads a top-level x- key from a YAML or JSON config. Load
// drops these keys, so a caller that acts on one reads it here.
func ExtensionValue(data []byte, key string) (string, bool, error) {
	var top map[string]any
	if err := yamlv3.Unmarshal(data, &top); err != nil {
		return "", false, fmt.Errorf("parse yaml: %w", err)
	}
	v, ok := top[key]
	if !ok {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, fmt.Errorf("%s: want a string, got %T", key, v)
	}
	return s, true, nil
}

// StarterListen picks the relay's address for an upstream: loopback, on the
// upstream's port plus 10000, the convention every example in this repo
// follows (5432 -> 15432). Loopback because a first config is for the
// person at this machine; exposing the relay is a decision, not a default.
func StarterListen(upstream string) (string, error) {
	_, portStr, err := net.SplitHostPort(upstream)
	if err != nil {
		return "", fmt.Errorf("upstream %q is not host:port: %w", upstream, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", fmt.Errorf("upstream %q has no valid port", upstream)
	}
	relay := port + 10000
	if relay > 65535 {
		relay = 15000 + port%1000
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(relay)), nil
}
