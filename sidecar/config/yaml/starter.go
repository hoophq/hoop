package yaml

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/daemon"
)

// StarterFile is the name the first-run screen writes the starter config to.
const StarterFile = "hoop-sidecar.yaml"

// Upstream is one backend the starter config can front: what speaks there,
// where it is, and how that was learned, so the file can say why it chose it.
type Upstream struct {
	Protocol string
	Addr     string
	// Source says how the upstream was found: "DATABASE_URL", "port 5432
	// is open", or "default" when nothing was.
	Source string
}

// StarterInput is what the first-run screen inferred from the machine.
type StarterInput struct {
	// Primary becomes the one active listener.
	Primary Upstream
	// Others are written as commented listeners, one uncomment away.
	Others []Upstream
	// AnalyzerProvider is the provider whose credential was seen in the
	// environment (anthropic, openai, vertex), or "" for none. The analyzer
	// block is written commented out either way: its key lives in a 0600
	// file, and the starter never reads or writes a secret.
	AnalyzerProvider string
	// KeyDir is the absolute directory the analyzer's key file goes in.
	// Absolute because credentials_file is read as written: a "~" in it is
	// not expanded, and the block would fail the moment it is uncommented.
	// Empty writes a placeholder the user must replace.
	KeyDir string
}

// starterProtocol is what the starter needs to know about one protocol: the
// guardrail that is safe to turn on for anyone, and why.
type starterProtocol struct {
	ops []string
	why string
}

// starterProtocols are the protocols a starter can front with listen and
// upstream alone. grpc, spanner and ssh need a block of their own
// (descriptors, a host key) that no inference can fill, so Starter refuses
// them rather than write a file that does not load.
var starterProtocols = map[string]starterProtocol{
	"postgres":   {[]string{"drop", "truncate"}, "refuse statements that destroy a table"},
	"mysql":      {[]string{"drop", "truncate"}, "refuse statements that destroy a table"},
	"mssql":      {[]string{"drop", "truncate"}, "refuse statements that destroy a table"},
	"oracle":     {[]string{"drop", "truncate"}, "refuse statements that destroy a table"},
	"clickhouse": {[]string{"drop", "truncate"}, "refuse statements that destroy a table"},
	"mongodb":    {[]string{"drop"}, "refuse commands that drop a collection or a database"},
	"http":       {[]string{"delete"}, "refuse DELETE requests"},
}

// StarterProtocol reports whether Starter can write a listener for p.
func StarterProtocol(p string) bool {
	_, ok := starterProtocols[p]
	return ok
}

// starterModels is the model each provider's commented block names. Any
// model the account serves works; these are a cheap, fast default for a
// classification call.
var starterModels = map[string]string{
	"anthropic": "claude-haiku-4-5",
	"openai":    "gpt-5-mini",
	"vertex":    "claude-sonnet-4-5@20250929",
}

// Starter writes a commented YAML config a first-time user can run as is:
// one listener in front of in.Primary, one guardrail and one data masking
// rule, which is what the free tier allows, and an AI analyzer block left
// commented out until a key file exists.
//
// The comments are the guide. Every protocol this build speaks is listed, so
// the file also answers "what else can it front".
func Starter(in StarterInput) ([]byte, error) {
	primary, ok := starterProtocols[in.Primary.Protocol]
	if !ok {
		return nil, fmt.Errorf("the starter config cannot front %q: it needs settings no default fills", in.Primary.Protocol)
	}
	listen, err := StarterListen(in.Primary.Addr)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w("# hoop sidecar starter config, written by `hoop start sidecar`.")
	w("#")
	w("# The sidecar sits between a client and a database or API. It reads every")
	w("# statement, applies the guardrails below, masks sensitive values on the")
	w("# way back, and records an audit trail.")
	w("#")
	w("#   check it:  hoop start sidecar --config %s --validate", StarterFile)
	w("#   run it:    hoop start sidecar --config %s", StarterFile)
	w("#")
	w("# Then point your client at %s instead of %s.", listen, in.Primary.Addr)
	w("#")
	w("# Protocols this build can inspect (the `protocol:` of a listener):")
	for _, p := range daemon.Protocols() {
		w("#   %-11s %s", p, daemon.ProtocolLabel(p))
	}
	w("")

	w("listeners:")
	w("  # Found by: %s.", in.Primary.Source)
	w("  - name: %s", in.Primary.Protocol)
	w("    protocol: %s", in.Primary.Protocol)
	w("    listen: %s", listen)
	w("    upstream: %s", in.Primary.Addr)
	w("    # The AI analyzer for this lane. Uncomment with the top-level")
	w("    # `analyzer:` block at the end of this file.")
	w("    # analyzer:")
	w("    #   trigger: {operations: [%s]}", starterTrigger(in.Primary.Protocol))
	w("    #   high: block")
	w("    #   medium: warn")
	w("    #   low: allow")
	for _, o := range in.Others {
		if !StarterProtocol(o.Protocol) {
			continue
		}
		ol, err := StarterListen(o.Addr)
		if err != nil {
			continue
		}
		w("")
		w("  # Also found (%s). Uncomment to inspect it too.", o.Source)
		w("  # - name: %s", o.Protocol)
		w("  #   protocol: %s", o.Protocol)
		w("  #   listen: %s", ol)
		w("  #   upstream: %s", o.Addr)
	}
	w("")

	w("# Guardrails decide what may run. Without a license, a sidecar enforces")
	w("# one guardrail rule and one data masking rule; a license lifts both caps.")
	w("guardrails:")
	w("  rules:")
	w("    - name: no-destructive-%s", starterRuleNoun(in.Primary.Protocol))
	w("      type: operation")
	w("      operations: [%s]", strings.Join(primary.ops, ", "))
	w("      message: %s is not allowed through the sidecar", strings.ToUpper(strings.Join(primary.ops, "/")))
	w("      # Why: %s.", primary.why)
	w("      # The operation comes from the parsed statement, not a text")
	w("      # match, so a string that only mentions DROP is not refused.")
	w("")

	w("# Data masking rewrites sensitive values in results before the client")
	w("# sees them. Swap the entity for another (CREDIT_CARD, PHONE_NUMBER,")
	w("# US_SSN...), or name columns instead: `columns: [email]`.")
	w("mask:")
	w("  rules:")
	w("    - name: emails")
	w("      entities: [EMAIL_ADDRESS]")
	w("      strategy: redact")
	w("")

	writeStarterAnalyzer(w, in.AnalyzerProvider, in.KeyDir)
	return []byte(b.String()), nil
}

// writeStarterAnalyzer writes the AI analyzer section, always commented
// out. Its key is read only from a file of mode 0600, and the starter does
// not create one: the comment carries the command that does. The lane's
// half of the analyzer is written inside the listener above.
func writeStarterAnalyzer(w func(string, ...any), provider, keyDir string) {
	w("# AI analyzer: a model rates each statement's risk, and the lane's")
	w("# high/medium/low map decides. It catches what no rule can express,")
	w("# like a DELETE or an UPDATE with no WHERE clause. The free tier does")
	w("# not cap it. Turn it on by uncommenting this block and the listener's.")
	if keyDir == "" {
		keyDir = "/path/to/keys"
	}
	if provider == "vertex" {
		w("#")
		w("# Google credentials are set in your environment. Fill in the project:")
		w("#")
		w("# analyzer:")
		w("#   provider: vertex")
		w("#   model: %s", starterModels["vertex"])
		w("#   extra: {project: my-gcp-project, region: global}")
		return
	}
	envKey := "ANTHROPIC_API_KEY"
	if provider == "openai" {
		envKey = "OPENAI_API_KEY"
	}
	if provider == "" {
		provider = "anthropic"
		w("#")
		w("# No model credential was found in your environment. With an")
		w("# Anthropic key in %s, store it in a file only you can read:", envKey)
	} else {
		w("#")
		w("# %s is set in your environment. Store it in a file only you can read:", envKey)
	}
	keyFile := keyDir + "/" + provider + ".key"
	w("#")
	w("#   mkdir -p %s && (umask 077; printf %%s \"$%s\" > %s)", keyDir, envKey, keyFile)
	w("#")
	w("# analyzer:")
	w("#   provider: %s", provider)
	w("#   model: %s", starterModels[provider])
	w("#   credentials_file: %s", keyFile)
}

// starterTrigger is the operations the analyzer looks at: the writes, where
// a model's judgement is worth a call.
func starterTrigger(protocol string) string {
	switch protocol {
	case "http":
		return "post, put, patch, delete"
	case "mongodb":
		return "insert, update, delete"
	}
	return "delete, update"
}

func starterRuleNoun(protocol string) string {
	if protocol == "http" {
		return "requests"
	}
	return "statements"
}

// StarterListen picks the relay's address for an upstream: loopback, on the
// upstream's port plus 10000, the convention every example in this repo
// follows (5432 -> 15432). Loopback because the starter is for the person at
// this machine; exposing the relay is a decision, not a default.
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
