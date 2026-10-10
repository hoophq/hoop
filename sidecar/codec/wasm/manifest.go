package wasm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
)

// Manifest is what `describe` returns: the plug-in's identity, the optional
// exports it carries and the limits the host applies to it. abi/ABI.md is
// the contract; the Go names here follow its keys.
type Manifest struct {
	ABI              int              `json:"abi"`
	Protocol         string           `json:"protocol"`
	Label            string           `json:"label"`
	Version          string           `json:"version,omitempty"`
	Capabilities     []string         `json:"capabilities,omitempty"`
	SQLDialect       string           `json:"sql_dialect,omitempty"`
	MaxReassembly    int              `json:"max_reassembly,omitempty"`
	Instances        string           `json:"instances,omitempty"`
	WASI             bool             `json:"wasi,omitempty"`
	CallTimeoutMS    int              `json:"call_timeout_ms,omitempty"`
	MemoryLimitPages uint32           `json:"memory_limit_pages,omitempty"`
	Options          []ManifestOption `json:"options,omitempty"`
}

// ManifestOption is one per-listener setting the plug-in accepts. The
// listener form renders it and `open` receives the chosen value as a string.
type ManifestOption struct {
	Name    string   `json:"name"`
	Label   string   `json:"label,omitempty"`
	Type    string   `json:"type"`
	Values  []string `json:"values,omitempty"`
	Default string   `json:"default,omitempty"`
	Help    string   `json:"help,omitempty"`
}

// The manifest's enumerations. Each is closed on purpose: a value outside
// it is a typo the author should hear about at load, not a silently
// ignored key.
const (
	CapDeny       = "deny"
	CapFilter     = "filter"
	CapRewrite    = "rewrite"
	CapCredential = "credential"
	CapContent    = "content"

	InstancesPerConnection = "per_connection"
	InstancesPerLane       = "per_lane"

	// ABIVersion is the one contract this host speaks.
	ABIVersion = 1

	// Defaults ABI.md names for the keys a manifest may leave out.
	DefaultMaxReassembly    = 8 << 20
	DefaultCallTimeoutMS    = 2000
	DefaultMemoryLimitPages = 1024

	// maxMemoryPages is the wasm32 ceiling: 65536 pages of 64 KiB is the
	// whole 4 GiB address space.
	maxMemoryPages = 65536
)

var capabilityNames = []string{CapDeny, CapFilter, CapRewrite, CapCredential, CapContent}

var optionTypes = []string{"string", "int", "bool", "enum"}

// protocolNamePattern is ABI.md's rule: an `x-` prefix, then the characters
// a listener form and a capability header can carry without quoting. The
// prefix is what lets the control plane grant every plug-in protocol under
// one `plugins` capability instead of naming each.
var protocolNamePattern = regexp.MustCompile(`^x-[a-z0-9_-]+$`)

// builtinProtocols are the names a plug-in may not claim even if the
// pattern allowed them: the constants libhoop defines, whether or not this
// binary registered a codec for them. inspect.Registered covers the codecs
// a custom binary linked in beyond these.
var builtinProtocols = []inspect.Protocol{
	inspect.Postgres, inspect.MSSQL, inspect.MySQL, inspect.ClickHouse,
	inspect.MongoDB, inspect.Oracle, inspect.HTTP, inspect.GRPC,
	inspect.Spanner, inspect.SSH,
}

// dialectOf maps the manifest's sql_dialect string to the lexer's Dialect.
// The lexer has no parser for its own names on purpose (its String method
// exists for logs), so the one mapping lives next to the one producer of
// the strings, which is this manifest.
func dialectOf(name string) (lexer.Dialect, bool) {
	switch name {
	case "postgres":
		return lexer.Postgres, true
	case "mysql":
		return lexer.MySQL, true
	case "mssql":
		return lexer.MSSQL, true
	case "clickhouse":
		return lexer.ClickHouse, true
	case "oracle":
		return lexer.Oracle, true
	case "googlesql":
		return lexer.GoogleSQL, true
	}
	return 0, false
}

// parseManifest decodes and validates a describe payload. Unknown keys are
// refused like every other payload: a misspelt `capabilites` would
// otherwise drop every capability and surface later as "export present
// without its capability", two steps away from the typo.
func parseManifest(raw []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("manifest: %w", err)
	}
	if dec.More() {
		return m, fmt.Errorf("manifest: trailing data after the JSON object")
	}
	if m.ABI != ABIVersion {
		return m, fmt.Errorf("manifest: abi %d is not supported, this host speaks abi %d", m.ABI, ABIVersion)
	}
	if !protocolNamePattern.MatchString(m.Protocol) {
		return m, fmt.Errorf("manifest: protocol %q must match %s", m.Protocol, protocolNamePattern)
	}
	if slices.Contains(builtinProtocols, inspect.Protocol(m.Protocol)) ||
		slices.Contains(inspect.Registered(), inspect.Protocol(m.Protocol)) {
		return m, fmt.Errorf("manifest: protocol %q is built into this binary", m.Protocol)
	}
	if strings.TrimSpace(m.Label) == "" {
		return m, fmt.Errorf("manifest: label is required")
	}
	for i, c := range m.Capabilities {
		if !slices.Contains(capabilityNames, c) {
			return m, fmt.Errorf("manifest: unknown capability %q (one of %s)", c, strings.Join(capabilityNames, ", "))
		}
		if slices.Contains(m.Capabilities[:i], c) {
			return m, fmt.Errorf("manifest: capability %q listed twice", c)
		}
	}
	if m.SQLDialect != "" {
		if _, ok := dialectOf(m.SQLDialect); !ok {
			return m, fmt.Errorf("manifest: unknown sql_dialect %q (postgres, mysql, mssql, clickhouse, oracle or googlesql)", m.SQLDialect)
		}
	}
	if m.MaxReassembly < 0 {
		return m, fmt.Errorf("manifest: max_reassembly %d is negative", m.MaxReassembly)
	}
	switch m.Instances {
	case "":
		m.Instances = InstancesPerConnection
	case InstancesPerConnection, InstancesPerLane:
	default:
		return m, fmt.Errorf("manifest: instances %q must be %s or %s", m.Instances, InstancesPerConnection, InstancesPerLane)
	}
	if m.CallTimeoutMS < 0 {
		return m, fmt.Errorf("manifest: call_timeout_ms %d is negative", m.CallTimeoutMS)
	}
	if m.MemoryLimitPages > maxMemoryPages {
		return m, fmt.Errorf("manifest: memory_limit_pages %d exceeds the wasm32 maximum of %d", m.MemoryLimitPages, maxMemoryPages)
	}
	for i, o := range m.Options {
		if o.Name == "" {
			return m, fmt.Errorf("manifest: options[%d] has no name", i)
		}
		for _, prev := range m.Options[:i] {
			if prev.Name == o.Name {
				return m, fmt.Errorf("manifest: option %q listed twice", o.Name)
			}
		}
		if !slices.Contains(optionTypes, o.Type) {
			return m, fmt.Errorf("manifest: option %q type %q must be one of %s", o.Name, o.Type, strings.Join(optionTypes, ", "))
		}
		if o.Type == "enum" {
			if len(o.Values) == 0 {
				return m, fmt.Errorf("manifest: enum option %q lists no values", o.Name)
			}
			if o.Default != "" && !slices.Contains(o.Values, o.Default) {
				return m, fmt.Errorf("manifest: enum option %q default %q is not one of its values", o.Name, o.Default)
			}
		} else if len(o.Values) > 0 {
			return m, fmt.Errorf("manifest: option %q is %s, only enum takes values", o.Name, o.Type)
		}
	}
	return m, nil
}

// hasCapability reports whether the manifest names cap.
func (m Manifest) hasCapability(cap string) bool {
	return slices.Contains(m.Capabilities, cap)
}

// maxReassembly, callTimeoutMS and memoryLimitPages apply ABI.md's defaults
// to an absent key. Zero is the absent value for all three: a plug-in
// cannot ask for no reassembly, no deadline or no memory.
func (m Manifest) maxReassembly() int {
	if m.MaxReassembly == 0 {
		return DefaultMaxReassembly
	}
	return m.MaxReassembly
}

func (m Manifest) callTimeoutMS() int {
	if m.CallTimeoutMS == 0 {
		return DefaultCallTimeoutMS
	}
	return m.CallTimeoutMS
}

func (m Manifest) memoryLimitPages() uint32 {
	if m.MemoryLimitPages == 0 {
		return DefaultMemoryLimitPages
	}
	return m.MemoryLimitPages
}
