package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/descriptors"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// CodecPlugin is a loaded codec plug-in: one module that decodes one
// protocol the build did not link (codec/wasm/abi/ABI.md). codec/wasm
// implements it; the root module cannot import that package, because wazero
// would end the one-dependency invariant, so the binary that links the host
// injects a loader through LoadCodecPlugin instead.
type CodecPlugin interface {
	// Protocol is the name the module's manifest declares, always x-
	// prefixed. The config entry that loaded it must name the same one.
	Protocol() inspect.Protocol
	// Manifest is the verified describe JSON, for the startup log and the
	// listener form.
	Manifest() []byte
	// NewCodec builds one codec per connection over the lane's options.
	// The codec implements io.Closer, and the gate closes it.
	NewCodec(options map[string]string) inspect.Codec
	// Close releases the module. Every codec built from it is closed first.
	Close() error
}

// LoadCodecPlugin instantiates a plug-in from its module bytes. Nil means
// this build loads no plug-ins: the daemon then refuses a config with a
// `plugins` list at startup, because a lane without its module would bind
// a port and decode nothing. sidecar/cmd/main.go and
// client/cmd/startsidecar.go set it.
var LoadCodecPlugin func(ctx context.Context, module []byte) (CodecPlugin, error)

// CodecTester drives `-codec-test`: the ABI's conformance run over a module
// and the author's fixtures, writing its report to out. Injected beside
// LoadCodecPlugin; nil means the flag reports that this build cannot test.
var CodecTester func(ctx context.Context, module []byte, fixtures [][]byte, out io.Writer) error

// CodecPluginConfig is one `plugins` entry: a module and the protocol it
// must declare.
//
// The entry names the protocol so the validator can check a lane against
// the config alone (the control plane never loads a module), and so a
// module swapped behind a URL cannot retarget a lane: the loader refuses a
// module whose manifest disagrees with the entry.
type CodecPluginConfig struct {
	// Protocol is the plug-in's protocol name, `x-` then [a-z0-9_-]. A
	// listener declares it the way it declares postgres.
	Protocol string `json:"protocol,omitempty"`
	// Module is the wasm module: a local path, or a URL with a scheme the
	// descriptors registry resolves (https in every build, gs when linked).
	Module string `json:"module,omitempty"`
	// SHA256 is the hex digest of the module bytes. Required for a URL,
	// because this process runs the code it fetched over the network and
	// the URL alone does not pin what it returns; optional for a file, and
	// the loader checks it whenever set.
	SHA256 string `json:"sha256,omitempty"`
}

// pluginProtocolName is the ABI's protocol grammar. The prefix is what keeps
// a plug-in from claiming a shipped name, and what the control plane grants
// by the `plugins` capability alone.
var pluginProtocolName = regexp.MustCompile(`^x-[a-z0-9_-]+$`)

// codecPluginFetchTimeout bounds one module download, the way the daemon
// bounds a grpc descriptor fetch: a stall is a startup that never
// completes, which an operator sees either way.
const codecPluginFetchTimeout = 2 * time.Minute

// errNoCodecPluginLoader is the refusal for a `plugins` list in a build
// without a loader. The plane never loads one; the sidecar binaries do.
var errNoCodecPluginLoader = errors.New("config has a \"plugins\" list but this build loads no codec plug-ins; " +
	"build github.com/hoophq/hoop/sidecar/cmd, or remove the list")

// validate checks the entries that the config alone can answer for.
// loadCodecPlugins reads the module itself, on the sidecar host.
func (p CodecPluginConfig) validate(i int) []string {
	where := fmt.Sprintf("plugins[%d]", i)
	if p.Protocol != "" {
		where = fmt.Sprintf("plugins[%d] (%s)", i, p.Protocol)
	}
	var problems []string
	switch {
	case p.Protocol == "":
		problems = append(problems, where+": no protocol")
	case !pluginProtocolName.MatchString(p.Protocol):
		problems = append(problems, fmt.Sprintf(
			"%s: protocol %q must start with \"x-\" followed by lowercase letters, digits, "+
				"\"_\" or \"-\"", where, p.Protocol))
	}
	if p.Module == "" {
		problems = append(problems, where+": no module")
	} else if descriptors.Scheme(p.Module) != "" && p.SHA256 == "" {
		problems = append(problems, where+": a module fetched from a URL needs its sha256; "+
			"the URL alone does not pin the code this process will run")
	}
	if p.SHA256 != "" {
		if _, err := decodeDigest(p.SHA256); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", where, err))
		}
	}
	return problems
}

func decodeDigest(s string) ([]byte, error) {
	raw, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(s)))
	if err != nil || len(raw) != sha256.Size {
		return nil, fmt.Errorf("sha256 %q is not a 64-character hex digest", s)
	}
	return raw, nil
}

// validatePlugins checks every entry and the set as a whole: one entry per
// protocol, since two modules claiming one name would make import order
// decide which a lane runs.
func (c *Config) validatePlugins() []string {
	var problems []string
	seen := map[string]int{}
	for i, p := range c.Plugins {
		problems = append(problems, p.validate(i)...)
		if p.Protocol == "" {
			continue
		}
		if first, dup := seen[p.Protocol]; dup {
			problems = append(problems, fmt.Sprintf(
				"plugins[%d]: protocol %q is already declared by plugins[%d]", i, p.Protocol, first))
			continue
		}
		seen[p.Protocol] = i
	}
	return problems
}

// pluginDeclared reports whether a `plugins` entry names the protocol. This
// is the config-level fact the validator and the control plane read; whether
// the module loads is the host's business.
func (c *Config) pluginDeclared(protocol string) bool {
	for _, p := range c.Plugins {
		if p.Protocol == protocol {
			return true
		}
	}
	return false
}

// isPluginLane reports whether a listener runs on a plug-in protocol.
func isPluginLane(lc ListenerConfig) bool {
	return analyzer.IsPluginProtocol(inspect.Protocol(lc.Protocol))
}

// CodecPluginInfo is one loaded plug-in as the startup log and the -validate
// report describe it.
type CodecPluginInfo struct {
	Protocol     string
	Version      string
	SHA256       string
	Capabilities []string
}

// String renders the info the way a -validate note and a log line share.
func (i CodecPluginInfo) String() string {
	caps := "none"
	if len(i.Capabilities) > 0 {
		caps = strings.Join(i.Capabilities, ", ")
	}
	version := i.Version
	if version == "" {
		version = "unversioned"
	}
	return fmt.Sprintf("codec plug-in %s %s, sha256 %s, capabilities: %s", i.Protocol, version, i.SHA256, caps)
}

// loadCodecPlugins reads, verifies and instantiates every `plugins` entry,
// once: a second call on a config that holds its plug-ins is a no-op, so
// Setup, Validate and Run can each make sure the set is loaded without
// knowing which of them ran first. The set is baseline (restart-bound) for
// the reloader, which carries the running instances onto each new document.
//
// The loop tries every entry before it reports the first failure, so a
// config with two broken modules reports both in one restart.
func (c *Config) loadCodecPlugins(ctx context.Context) error {
	if c.codecPlugins != nil || len(c.Plugins) == 0 {
		return nil
	}
	if LoadCodecPlugin == nil {
		return errNoCodecPluginLoader
	}
	roots, err := loadTrustRoots(c.Trust)
	if err != nil {
		return err
	}
	client := outboundHTTPClient(roots, "plugins")
	loaded := make(map[inspect.Protocol]CodecPlugin, len(c.Plugins))
	infos := make([]CodecPluginInfo, 0, len(c.Plugins))
	var problems []string
	for i, entry := range c.Plugins {
		where := fmt.Sprintf("plugins[%d] (%s)", i, entry.Protocol)
		plugin, info, err := loadCodecPlugin(ctx, entry, client)
		if err != nil {
			problems = append(problems, where+": "+err.Error())
			continue
		}
		loaded[plugin.Protocol()] = plugin
		infos = append(infos, info)
	}
	if len(problems) > 0 {
		for _, p := range loaded {
			_ = p.Close()
		}
		return ConfigProblems(problems)
	}
	c.codecPlugins = loaded
	c.codecPluginInfo = infos
	return nil
}

// loadCodecPlugin loads one entry: bytes, digest, module, manifest, and the
// analyzer builder the lane's `analyzer` block will reach.
func loadCodecPlugin(ctx context.Context, entry CodecPluginConfig, client *http.Client) (CodecPlugin, CodecPluginInfo, error) {
	module, err := readCodecModule(ctx, entry.Module, client)
	if err != nil {
		return nil, CodecPluginInfo{}, err
	}
	sum := sha256.Sum256(module)
	digest := hex.EncodeToString(sum[:])
	if entry.SHA256 != "" {
		want, _ := decodeDigest(entry.SHA256)
		if !bytes.Equal(want, sum[:]) {
			return nil, CodecPluginInfo{}, fmt.Errorf(
				"sha256 mismatch: the module is %s, the config expects %s", digest, strings.ToLower(entry.SHA256))
		}
	}
	plugin, err := LoadCodecPlugin(ctx, module)
	if err != nil {
		return nil, CodecPluginInfo{}, err
	}
	if got := string(plugin.Protocol()); got != entry.Protocol {
		_ = plugin.Close()
		return nil, CodecPluginInfo{}, fmt.Errorf(
			"the module declares protocol %q; the entry expects %q and names the protocol its lanes run, "+
				"so the loader refuses a module for another one", got, entry.Protocol)
	}
	info, err := describeCodecPlugin(plugin, digest)
	if err != nil {
		_ = plugin.Close()
		return nil, CodecPluginInfo{}, err
	}
	if err := installCodecPluginBuilder(plugin); err != nil {
		_ = plugin.Close()
		return nil, CodecPluginInfo{}, err
	}
	return plugin, info, nil
}

// readCodecModule reads the module bytes from a path or a URL the
// descriptors registry resolves, the way the daemon reads a grpc lane's
// descriptor set.
func readCodecModule(ctx context.Context, module string, client *http.Client) ([]byte, error) {
	if descriptors.Scheme(module) == "" {
		return os.ReadFile(module)
	}
	ctx, cancel := context.WithTimeout(ctx, codecPluginFetchTimeout)
	defer cancel()
	return descriptors.Fetch(ctx, module, client)
}

// describeCodecPlugin reads the manifest facts the log reports. The host
// verified the manifest at load, so a manifest this cannot decode is a host
// bug: the loader refuses the module and reports it.
func describeCodecPlugin(plugin CodecPlugin, digest string) (CodecPluginInfo, error) {
	var m struct {
		Version      string   `json:"version"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(plugin.Manifest(), &m); err != nil {
		return CodecPluginInfo{}, fmt.Errorf("the loaded module's manifest does not decode: %w", err)
	}
	return CodecPluginInfo{
		Protocol:     string(plugin.Protocol()),
		Version:      m.Version,
		SHA256:       digest,
		Capabilities: m.Capabilities,
	}, nil
}

// installCodecPluginBuilder gives the protocol its analyzer content
// builder: the module's own rendering when its codec offers Content (the
// `content` capability), the generic one otherwise. A lane whose protocol
// has no builder classifies nothing without a trace, which is why Validate
// refuses an analyzer on such a lane; a plug-in lane always has one, so
// that check passes for it on the strength of this call.
//
// The loader builds the codec kept for rendering with no options; it never
// sees a byte, and the plug-in's Close releases it.
func installCodecPluginBuilder(plugin CodecPlugin) error {
	p := plugin.Protocol()
	codec := plugin.NewCodec(nil)
	if codec == nil {
		return errors.New("the module built no codec")
	}
	var b analyzer.Builder = analyzer.GenericBuilder{Protocol_: p}
	if r, ok := codec.(analyzer.ContentRenderer); ok {
		b = analyzer.PluginBuilder{Protocol_: p, Renderer: r}
	} else if closer, ok := codec.(io.Closer); ok {
		_ = closer.Close()
	}
	return analyzer.SetPluginBuilder(p, b)
}

// closeCodecPlugins releases every loaded module, after the lanes that run
// them are down. It joins every error into the one it returns.
func (c *Config) closeCodecPlugins() error {
	var errs []error
	for p, plugin := range c.codecPlugins {
		if err := plugin.Close(); err != nil {
			errs = append(errs, fmt.Errorf("plug-in %s: %w", p, err))
		}
	}
	c.codecPlugins = nil
	c.codecPluginInfo = nil
	return errors.Join(errs...)
}

// codecPluginFor returns the loaded plug-in a listener runs, or an error for
// a plug-in lane whose module is not loaded: a lane built without it would
// bind a port and decode nothing.
func (c *Config) codecPluginFor(lc ListenerConfig) (CodecPlugin, error) {
	if !isPluginLane(lc) {
		return nil, nil
	}
	plugin, ok := c.codecPlugins[inspect.Protocol(lc.Protocol)]
	if !ok {
		return nil, fmt.Errorf("protocol %q is a plug-in protocol and no loaded plug-in declares it; "+
			"a lane cannot run without its module", lc.Protocol)
	}
	return plugin, nil
}

// codecPluginFactory is the lane's codec factory for a plug-in lane: one
// codec per connection over the listener's `plugin` options.
func codecPluginFactory(plugin CodecPlugin, options map[string]string) func() inspect.Codec {
	return func() inspect.Codec { return plugin.NewCodec(options) }
}

// codecPluginNote is the loaded plug-in's description for a lane report.
// The protocol is a loaded one: buildLanes refused the lane otherwise.
func (c *Config) codecPluginNote(protocol string) string {
	for _, info := range c.codecPluginInfo {
		if info.Protocol == protocol {
			return info.String()
		}
	}
	return "codec plug-in " + protocol + " (not loaded)"
}

// errNoCodecTester is the refusal for -codec-test in a build without the
// conformance runner.
var errNoCodecTester = errors.New("this build cannot test codec plug-ins; " +
	"build github.com/hoophq/hoop/sidecar/cmd")

// CodecTest runs the ABI conformance checks over the module at path and the
// fixture files, writing the report to out. The -codec-test flag calls it,
// and `hoop start sidecar` offers the same flag through the export.
func CodecTest(ctx context.Context, path string, fixturePaths []string, out io.Writer) error {
	if CodecTester == nil {
		return errNoCodecTester
	}
	module, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("codec-test: %w", err)
	}
	fixtures := make([][]byte, 0, len(fixturePaths))
	for _, p := range fixturePaths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("codec-test: fixture %s: %w", p, err)
		}
		fixtures = append(fixtures, raw)
	}
	return CodecTester(ctx, module, fixtures, out)
}
