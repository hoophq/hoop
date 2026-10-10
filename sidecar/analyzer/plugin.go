package analyzer

import (
	"fmt"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// pluginProtocolPrefix marks a protocol a codec plug-in declares. The ABI
// reserves the prefix (codec/wasm/abi/ABI.md), and the registry treats such
// a protocol differently from a shipped one: the loader installs its
// builder at plug-in load, after package init, so it must be replaceable.
const pluginProtocolPrefix = "x-"

// IsPluginProtocol reports whether p carries the plug-in prefix. The daemon,
// the registry and the control plane all decide on the same test, so it is
// one function.
func IsPluginProtocol(p inspect.Protocol) bool {
	return strings.HasPrefix(string(p), pluginProtocolPrefix)
}

// SetPluginBuilder installs the builder for a plug-in protocol, replacing
// any earlier one. The host loads a plug-in once per process start, but
// the -validate path and Run share one process in tests and an embedder
// may load a module twice, so the duplicate-registration panic
// RegisterBuilder keeps for shipped protocols would turn a second load
// into a crash. It refuses a shipped protocol name: a module cannot take
// over postgres by registering late.
func SetPluginBuilder(p inspect.Protocol, b Builder) error {
	if !IsPluginProtocol(p) {
		return fmt.Errorf("sidecar/analyzer: %q is not a plug-in protocol (no %q prefix)", p, pluginProtocolPrefix)
	}
	if b == nil {
		return fmt.Errorf("sidecar/analyzer: SetPluginBuilder(%q) called with nil", p)
	}
	if b.Protocol() != p {
		return fmt.Errorf("sidecar/analyzer: builder answers for %q; the registration is for %q", b.Protocol(), p)
	}
	builderMu.Lock()
	defer builderMu.Unlock()
	builders[p] = b
	return nil
}

// GenericBuilder renders a plug-in protocol's statement from the structured
// facts every codec reports, for a module that declares no `content`
// capability. The native verb reaches the model when the module put it in
// metadata["<protocol>.verb"], the ABI's convention.
type GenericBuilder struct {
	Protocol_ inspect.Protocol
}

// Protocol implements Builder.
func (b GenericBuilder) Protocol() inspect.Protocol { return b.Protocol_ }

// Build renders the statement text under a header of what the codec
// derived, the same layout SQLBuilder uses so one prompt reads every
// protocol alike. Nothing to classify when the text is empty.
func (b GenericBuilder) Build(stmt inspect.Statement, maxBytes int) (Content, bool) {
	text := strings.TrimSpace(stmt.Text)
	if text == "" {
		return Content{}, false
	}
	var sb strings.Builder
	sb.WriteString("Protocol: ")
	sb.WriteString(string(b.Protocol_))
	sb.WriteString("\nOperation: ")
	sb.WriteString(string(stmt.Operation))
	if verb := stmt.Metadata[string(b.Protocol_)+".verb"]; verb != "" {
		sb.WriteString("\nVerb: ")
		sb.WriteString(verb)
	}
	if len(stmt.Tables) > 0 {
		sb.WriteString("\nTables: ")
		sb.WriteString(strings.Join(stmt.Tables, ", "))
	}
	sb.WriteString("\n\n")
	sb.WriteString(Truncate(text, maxBytes))
	// The SQL shape key: a plug-in protocol carrying SQL in its text gets
	// the literal stripping for free, and one that does not loses nothing
	// but a few cache hits.
	return Content{Text: sb.String(), CacheKey: sqlCacheKey(stmt)}, true
}

// ContentRenderer is the method a codec offers when its plug-in declares the
// `content` capability: the module renders its own statements for the
// model, because it knows which bytes are the operation and which are the
// payload. The signature mirrors Builder.Build with the Content struct
// flattened, so a codec in another module satisfies it structurally.
type ContentRenderer interface {
	Content(stmt inspect.Statement, maxBytes int) (text, cacheKey string, ok bool)
}

// PluginBuilder adapts a codec's Content method into a Builder. The
// Renderer is one codec instance kept for rendering alone; it never sees
// connection bytes, so per-connection state cannot leak through it.
type PluginBuilder struct {
	Protocol_ inspect.Protocol
	Renderer  ContentRenderer
}

// Protocol implements Builder.
func (b PluginBuilder) Protocol() inspect.Protocol { return b.Protocol_ }

// Build implements Builder over the codec's renderer.
func (b PluginBuilder) Build(stmt inspect.Statement, maxBytes int) (Content, bool) {
	if b.Renderer == nil {
		return Content{}, false
	}
	text, key, ok := b.Renderer.Content(stmt, maxBytes)
	if !ok {
		return Content{}, false
	}
	return Content{Text: text, CacheKey: key}, true
}
