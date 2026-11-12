package sidecartui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/client/cmd/sidecardemo"
	"github.com/hoophq/hoop/sidecar/analyzer"
	configyaml "github.com/hoophq/hoop/sidecar/config/yaml"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/pii/alcatraz"
	"github.com/hoophq/hoop/sidecar/policy"
)

// draft is the config the setup screens edit. It is turned into a
// daemon.Config only to validate or save, so the file is always the
// struct, marshalled, and never text this package assembled.
type draft struct {
	// demo is set when the person picked the demo: every built-in demo at
	// once, one listener each, with protocol demoProtocol.
	demo     bool
	protocol string
	listener *listenerForm // nil for the demo, whose listeners are fixed
	// demoHTTP and demoPG are the demo listeners and their upstreams, moved
	// off the preferred ports when another program holds them.
	demoHTTP, demoPG sidecardemo.Ports
	// license is the license file the config names under its license key,
	// "" for the free tier.
	license string

	guardMode string
	rules     []policy.Rule
	masks     []alcatraz.Rule
	pii       piiDraft
	an        analyzerDraft
	file      string
}

type piiDraft struct {
	entities, ignored, allow []string
	threshold, language      string
}

func (p piiDraft) empty() bool {
	return len(p.entities) == 0 && len(p.ignored) == 0 && len(p.allow) == 0 && p.threshold == "" && p.language == ""
}

type analyzerDraft struct {
	on                       bool
	provider, model, keyFile string
	project, region          string
	trigger                  []string
	high, medium, low        string
	keyDir                   string
}

// starterModels is the model each provider's block names: a cheap, fast
// default for a classification call. Any model the account serves works.
var starterModels = map[string]string{
	hostedProvider: analyzer.HostedModel,
	"anthropic":    "claude-haiku-4-5",
	"openai":       "gpt-5-mini",
	"gemini":       "gemini-2.5-flash",
	"vertex":       "claude-sonnet-4-5@20250929",
}

// hostedProvider is the model Hoop hosts (DEP-161). The config selects it
// with use_hoop_llm_provider, never by provider name, so the wizard shows it
// as a provider and writes the flag. It needs no key, which is why it is the
// default for a person who has no LLM key of their own.
const hostedProvider = "hoop"

// hostedPIINote says what choosing the hosted model sends where.
const hostedPIINote = "Statement text, which can contain PII, is sent to Hoop to be rated."

var providerLabel = map[string]string{hostedProvider: "Hoop-hosted (" + analyzer.HostedModel + ", no key)"}

// analyzerProviders is the provider list the form offers: the hosted model
// first when the binary links it, then the rest.
func analyzerProviders() []string {
	var out []string
	rest := analyzer.RegisteredProviders()
	if slices.Contains(rest, hostedProvider) {
		out = append(out, hostedProvider)
	}
	for _, p := range rest {
		if p != hostedProvider {
			out = append(out, p)
		}
	}
	return out
}

// needsKey reports whether provider reads a key file.
func needsKey(provider string) bool { return provider != hostedProvider && provider != "vertex" }

var keyEnv = map[string]string{"anthropic": "ANTHROPIC_API_KEY", "openai": "OPENAI_API_KEY", "gemini": "GEMINI_API_KEY"}

// defaultUpstream is where a protocol's backend usually listens, used when
// nothing was found on this machine.
var defaultUpstream = map[string]string{
	"postgres": "127.0.0.1:5432", "mysql": "127.0.0.1:3306", "mssql": "127.0.0.1:1433",
	"oracle": "127.0.0.1:1521", "mongodb": "127.0.0.1:27017", "clickhouse": "127.0.0.1:8123",
	"http": "127.0.0.1:8080", "grpc": "127.0.0.1:50051", "spanner": "spanner.googleapis.com:443",
	"mcp": "http://127.0.0.1:8000/mcp",
}

// ---- operations and rule types per protocol ----------------------------------

func opsFor(protocol string) []string {
	var out []string
	for _, op := range inspect.Operations() {
		s := string(op)
		var http, ssh bool
		switch op {
		case inspect.OpGet, inspect.OpPost, inspect.OpPut, inspect.OpPatch, inspect.OpHead,
			inspect.OpOptions, inspect.OpConnect, inspect.OpTrace, inspect.OpWSMessage, inspect.OpWSClose:
			http = true
		}
		ssh = op == inspect.OpExecLine || op == inspect.OpEnvSet || strings.HasPrefix(s, "sftp_")
		switch protocol {
		case demoProtocol:
			// The demo runs HTTP and SQL listeners, and a rule applies to both.
			if !ssh && op != inspect.OpUnknown {
				out = append(out, s)
			}
		case "http":
			if http || op == inspect.OpDelete {
				out = append(out, s)
			}
		case "ssh":
			if ssh {
				out = append(out, s)
			}
		default:
			if !http && !ssh && op != inspect.OpUnknown {
				out = append(out, s)
			}
		}
	}
	return out
}

var ruleTypeLabel = map[string]string{
	string(policy.MatchDenyWords):    "Deny words",
	string(policy.MatchPattern):      "Regex pattern",
	string(policy.MatchOperation):    "Operations",
	string(policy.MatchTable):        "Tables",
	string(policy.MatchProcedure):    "Stored procedures",
	string(policy.MatchPII):          "Sensitive data in the statement",
	string(policy.MatchHTTPResource): "HTTP resources",
	string(policy.MatchHTTPStatus):   "HTTP status",
	string(policy.MatchHTTPHeader):   "HTTP headers",
	string(policy.MatchGRPCStatus):   "gRPC status",
	string(policy.MatchMetadata):     "Statement metadata",
	string(policy.MatchMCPTool):      "MCP tools",
}

// ruleTypesFor lists the rule types a lane of protocol can evaluate, in
// policy.RuleTypes order. ai_analysis is left out: it is deprecated in
// favour of the analyzer, which has its own screen.
// On grpc and spanner, operation, table and pii rules read decoded
// payloads, so they are offered only when capture is on.
func ruleTypesFor(protocol string, captures bool) []string {
	var out []string
	rpc := protocol == "grpc" || protocol == "spanner"
	for _, t := range policy.RuleTypes() {
		switch t {
		case policy.MatchAIAnalysis:
			continue
		case policy.MatchOperation, policy.MatchPII:
			if rpc && !captures {
				continue
			}
		case policy.MatchTable, policy.MatchProcedure:
			if protocol == "http" || protocol == "ssh" || protocol == demoProtocol || (rpc && !captures) {
				continue
			}
		case policy.MatchHTTPResource, policy.MatchHTTPStatus, policy.MatchHTTPHeader:
			if protocol != "http" {
				continue
			}
		case policy.MatchGRPCStatus:
			if protocol != "grpc" && protocol != "spanner" {
				continue
			}
		case policy.MatchMCPTool:
			if protocol != "mcp" {
				continue
			}
		}
		out = append(out, string(t))
	}
	return out
}

func ops(s ...string) []inspect.Operation {
	out := make([]inspect.Operation, len(s))
	for i, x := range s {
		out[i] = inspect.Operation(x)
	}
	return out
}

func opStrings(o []inspect.Operation) []string {
	out := make([]string, len(o))
	for i, x := range o {
		out[i] = string(x)
	}
	return out
}

// ---- defaults -----------------------------------------------------------------

// isDemo reports whether this draft is one of the built-in demos.
func (d *draft) isDemo() bool { return d.demo }

// The demo's listeners, by name. The tour matches audit events to a demo by
// the listener that recorded them.
const (
	demoHTTPListener = "demo-api"
	demoPGListener   = "demo-postgres"
)

// demoProtocol is the draft protocol of the demo, which runs several. The
// editors read it as "what every demo listener accepts".
const demoProtocol = "demo"

// demoTriggers are the operations each demo listener sends to the analyzer:
// the writes of its protocol. The demo fixes them, as it fixes the listeners.
var demoTriggers = map[string][]string{
	"http":     {"post", "put", "patch", "delete"},
	"postgres": {"delete", "update"},
}

// newDraft is the config the setup screens start from: one listener, the
// guardrail and the masking rule the free tier allows, and the analyzer.
// The analyzer uses the person's own provider when one is detected, on only
// when its key file already exists, since a config that names a missing key
// does not start. With none detected it uses the model Hoop hosts, on.
//
// id is a menu choice: a protocol name, or demoID. The demo fixes its own
// listeners and ports.
func newDraft(id string, m machine) (*draft, error) {
	d := &draft{demo: id == demoID, protocol: id, file: configyaml.StarterFile}
	if d.demo {
		// Free ports, chosen now: a demo that cannot bind fails at boot,
		// after the person was told it was ready. The database is hoop's,
		// across the network: only its listener binds here.
		d.protocol = demoProtocol
		taken := map[string]bool{}
		d.demoHTTP.Listen = freeFrom(sidecardemo.ListenAddr, taken)
		taken[d.demoHTTP.Listen] = true
		d.demoHTTP.API = freeFrom(sidecardemo.Addr, taken)
		taken[d.demoHTTP.API] = true
		d.demoPG.Listen = freeFrom(sidecardemo.PGListenAddr, taken)
		d.demoPG.API = sidecardemo.PGAddr
	} else {
		protocol := id
		upstream := defaultUpstream[protocol]
		var source string
		if f, ok := m.foundFor(protocol); ok {
			upstream, source = f.addr, f.source
		}
		prefill := map[string]string{"name": protocol, "upstream": upstream}
		if l, err := configyaml.StarterListen(upstream); err == nil {
			prefill["listen"] = l
		} else if protocol == "ssh" {
			prefill["listen"] = "127.0.0.1:12222"
		} else if protocol == "mcp" {
			// An mcp upstream is a URL, which StarterListen does not read.
			prefill["listen"] = "127.0.0.1:18000"
		}
		// The suggested port moves when another program holds it, so the
		// default boots on this machine as it is.
		prefill["listen"] = freeFrom(prefill["listen"], map[string]bool{})
		if protocol == "spanner" {
			prefill["upstream_tls"] = "on"
		}
		// A backend found on another machine (an RDS host in DATABASE_URL)
		// is reached across a network: the relay originates TLS to it by
		// default, so the credentials a client sends do not cross it in
		// the clear. The person can still turn it off.
		if host, _, err := net.SplitHostPort(upstream); err == nil && source != "" && !isLoopbackHost(host) {
			prefill["upstream_tls"] = "on"
		}
		lf, err := newListenerForm(protocol, prefill, source)
		if err != nil {
			return nil, err
		}
		d.listener = lf
	}
	switch d.protocol {
	case demoProtocol:
		// One rule for every demo listener: the free tier enforces one per
		// process. It leaves UPDATE and POST to the analyzer.
		d.rules = []policy.Rule{{Name: "no-destructive", Type: policy.MatchOperation, Operations: ops("delete", "drop", "truncate"),
			Message: "DELETE, DROP and TRUNCATE are not allowed through the sidecar"}}
	case "http":
		d.rules = []policy.Rule{{Name: "no-deletes", Type: policy.MatchOperation, Operations: ops("delete"),
			Message: "DELETE is not allowed through the sidecar"}}
	case "mongodb":
		d.rules = []policy.Rule{{Name: "no-drops", Type: policy.MatchOperation, Operations: ops("drop"),
			Message: "dropping a collection is not allowed through the sidecar"}}
	case "ssh":
		d.rules = []policy.Rule{{Name: "no-file-removal", Type: policy.MatchOperation, Operations: ops("sftp_remove", "sftp_rmdir"),
			Message: "removing files is not allowed through the sidecar"}}
	case "grpc", "spanner":
		// A gRPC lane reads operations, tables and data only from decoded
		// payloads, which need descriptors no default can name; the person
		// adds a rule once the listener captures them.
	case "mcp":
		r := policy.Rule{Name: "no-delete-tools", Type: policy.MatchMCPTool,
			Message: "delete tools are not allowed through the sidecar"}
		r.Tools = []string{"*delete*"}
		d.rules = []policy.Rule{r}
	default:
		d.rules = []policy.Rule{{Name: "no-destructive-statements", Type: policy.MatchOperation, Operations: ops("drop", "truncate"),
			Message: "DROP and TRUNCATE are not allowed through the sidecar"}}
	}
	// grpc and spanner mask only through grpc.descriptors, which no
	// default can supply. An mcp lane does not mask yet.
	switch d.protocol {
	case "grpc", "spanner", "mcp":
	case "ssh":
		// An ssh lane rewrites bytes in place, so the replacement must be
		// as long as the value: mask is the one strategy it carries.
		d.masks = []alcatraz.Rule{{Name: "emails", Entities: []string{"EMAIL_ADDRESS"}, Strategy: alcatraz.StrategyMask}}
	default:
		d.masks = []alcatraz.Rule{{Name: "emails", Entities: []string{"EMAIL_ADDRESS"}, Strategy: alcatraz.StrategyRedact}}
	}

	keyDir := m.keyDir
	if keyDir == "" {
		keyDir = "/path/to/keys"
	}
	registered := analyzer.RegisteredProviders()
	provider := m.provider
	if provider == "" {
		// A key file left from an earlier setup is the person's own LLM.
		for _, p := range []string{"anthropic", "openai", "gemini"} {
			if slices.Contains(registered, p) && keyFileStatus(filepath.Join(keyDir, p+".key")) == nil {
				provider = p
				break
			}
		}
	}
	if provider == "" || !slices.Contains(registered, provider) {
		provider = hostedProvider
		if !slices.Contains(registered, provider) {
			provider = "anthropic"
		}
	}
	keyProvider := provider
	if !needsKey(provider) {
		keyProvider = "anthropic"
	}
	d.an = analyzerDraft{provider: provider, model: starterModels[provider], keyDir: keyDir,
		keyFile: filepath.Join(keyDir, keyProvider+".key"), region: "global",
		high: "block", medium: "warn", low: "allow"}
	switch d.protocol {
	case "http":
		d.an.trigger = []string{"post", "put", "patch", "delete"}
	case "mongodb":
		d.an.trigger = []string{"insert", "update", "delete"}
	case "ssh":
		d.an.trigger = []string{"exec_line"}
	default:
		d.an.trigger = []string{"delete", "update"}
	}
	// An mcp lane refuses an analyzer; a grpc or spanner lane takes one
	// only with grpc.capture_payload and descriptors, which no default can
	// name, so the person turns it on once those are set.
	d.an.on = !slices.Contains([]string{"mcp", "grpc", "spanner"}, d.protocol) && provider != "vertex" &&
		(provider == hostedProvider || keyFileStatus(d.an.keyFile) == nil)
	return d, nil
}

// keyFileStatus is nil when path is a key file the analyzer will accept.
func keyFileStatus(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return errors.New("missing")
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("mode %04o, must be 0600 or stricter", fi.Mode().Perm())
	}
	return nil
}

// ---- the config -------------------------------------------------------------

// listenerValues are the listeners the config runs: the person's one, or one
// per demo.
func (d *draft) listenerValues() ([]daemon.ListenerConfig, error) {
	if !d.isDemo() {
		l, err := d.listener.value()
		return []daemon.ListenerConfig{l}, err
	}
	// The demo database is on RDS, whose certificates chain to Amazon's
	// own roots and to nothing in a host trust store. The relay verifies
	// it against the bundle the CLI ships and writes there before boot
	// (sidecardemo.EnsureCA), never by skipping verification.
	ca, err := sidecardemo.CAPath()
	if err != nil {
		return nil, err
	}
	return []daemon.ListenerConfig{
		{Name: demoHTTPListener, Protocol: "http", Listen: d.demoHTTP.Listen, Upstream: d.demoHTTP.API},
		{Name: demoPGListener, Protocol: "postgres", Listen: d.demoPG.Listen, Upstream: d.demoPG.API,
			UpstreamTLS: &daemon.TLSConfig{CAFile: ca}},
	}, nil
}

// config builds the daemon.Config and the comments Render writes around it.
func (d *draft) config() (*daemon.Config, configyaml.RenderOptions, error) {
	ls, err := d.listenerValues()
	if err != nil {
		return nil, configyaml.RenderOptions{}, err
	}
	cfg := &daemon.Config{License: d.license}
	if d.an.on {
		switch d.an.provider {
		case hostedProvider:
			// Explicit, not the license default: an Enterprise license
			// refuses the default, and the file then says where statements go.
			on := true
			cfg.Analyzer = &daemon.AnalyzerConfig{UseHoopLLMProvider: &on}
		case "vertex":
			cfg.Analyzer = &daemon.AnalyzerConfig{Provider: d.an.provider, Model: d.an.model,
				Extra: daemon.ProviderExtra{"project": d.an.project, "region": d.an.region}}
		default:
			cfg.Analyzer = &daemon.AnalyzerConfig{Provider: d.an.provider, Model: d.an.model,
				CredentialsFile: d.an.keyFile}
		}
		for i := range ls {
			l := &ls[i]
			trigger := d.an.trigger
			if d.isDemo() {
				trigger = demoTriggers[l.Protocol]
			}
			l.Analyzer = &daemon.LaneAnalyzerConfig{HighRisk: d.an.high, MediumRisk: d.an.medium, LowRisk: d.an.low}
			if len(trigger) > 0 {
				l.Analyzer.Trigger = &policy.AITrigger{Operations: ops(trigger...)}
			}
		}
	}
	// An http_header rule may only name a header the listener captures.
	// The person named it in the rule, so capturing it is what they asked
	// for; making them also edit the listener would be a second step for
	// one decision.
	for _, r := range d.rules {
		for _, h := range r.HeaderNames() {
			l := &ls[0]
			if l.HTTP == nil {
				l.HTTP = &daemon.HTTPCodecConfig{}
			}
			if !slices.Contains(l.HTTP.Headers, h) {
				l.HTTP.Headers = append(l.HTTP.Headers, h)
			}
		}
	}
	cfg.Listeners = ls
	if len(d.rules) > 0 || d.guardMode != "" {
		cfg.Guardrails = &daemon.GuardrailsConfig{Mode: d.guardMode, Rules: d.rules}
	}
	if len(d.masks) > 0 {
		b, err := json.Marshal(d.masks)
		if err != nil {
			return nil, configyaml.RenderOptions{}, err
		}
		cfg.Mask = &daemon.MaskConfig{Rules: b}
	}
	if !d.pii.empty() {
		sec := map[string]any{}
		if len(d.pii.entities) > 0 {
			sec["entities"] = d.pii.entities
		}
		if len(d.pii.ignored) > 0 {
			sec["ignored"] = d.pii.ignored
		}
		if len(d.pii.allow) > 0 {
			sec["allow_list"] = d.pii.allow
		}
		if d.pii.threshold != "" {
			t, err := strconv.ParseFloat(d.pii.threshold, 64)
			if err != nil || t < 0 || t > 1 {
				return nil, configyaml.RenderOptions{}, fmt.Errorf("PII threshold %q: want a number from 0 to 1", d.pii.threshold)
			}
			sec["threshold"] = t
		}
		if d.pii.language != "" {
			sec["language"] = d.pii.language
		}
		b, err := json.Marshal(sec)
		if err != nil {
			return nil, configyaml.RenderOptions{}, err
		}
		cfg.PII = b
	}
	return cfg, d.renderOptions(ls), nil
}

func (d *draft) renderOptions(ls []daemon.ListenerConfig) configyaml.RenderOptions {
	l := ls[0]
	backend := "the " + daemon.ProtocolLabel(l.Protocol) + " backend"
	if d.isDemo() {
		backend = "each demo backend"
	}
	file := d.file
	o := configyaml.RenderOptions{
		Header: []string{
			"hoop sidecar config, written by the `hoop start sidecar` setup screen.",
			"",
			"The sidecar sits between a client and " + backend + ". It reads every",
			"statement, applies the guardrails, masks sensitive values on the way back,",
			"and records an audit trail.",
			"",
			"  check it:  hoop start sidecar --config " + file + " --validate",
			"  run it:    hoop start sidecar --config " + file,
		},
		Comments: map[string][]string{
			"listeners":  {"Clients connect to `listen`; the sidecar forwards to `upstream`."},
			"license":    {"The license this sidecar runs under: the file holding the document hoop issued.", "It lifts the free tier's one-rule caps."},
			"guardrails": {"What may run. Without a license a sidecar enforces one guardrail rule", "and one data masking rule; a license lifts both caps."},
			"mask":       {"Rewrites sensitive values in results before the client sees them."},
			"pii":        {"Which sensitive-data types the detector knows. Absent means all of them."},
			"analyzer":   {"A model rates each statement's risk; the listener's high/medium/low", "map decides. The free tier does not cap it."},
		},
	}
	if d.isDemo() {
		o.Header = append(o.Header, "",
			"This is the demo, every one at once. Listener "+demoHTTPListener+" fronts an invented API the",
			"hoop CLI serves at "+d.demoHTTP.API+" while the sidecar runs, because of the "+configyaml.DemoAPIKey+" key.",
			"Listener "+demoPGListener+" fronts hoop's public demo database at "+d.demoPG.API+", a read-only",
			"copy of the dellstore sample shop; the "+configyaml.DemoPostgresKey+" key turns its tour on, and the",
			"CLI keeps the Amazon RDS CA that verifies it where upstream_tls.ca_file points.",
			"The guardrail and the mask rule below apply to both. Try:", "")
		for _, c := range sidecardemo.TryCommands(sidecardemo.KindHTTP, d.demoHTTP, d.an.on) {
			o.Header = append(o.Header, "  "+c)
		}
		for _, c := range sidecardemo.TryCommands(sidecardemo.KindPostgres, d.demoPG, d.an.on) {
			o.Header = append(o.Header, "  "+c)
		}
		o.Extensions = []configyaml.Extension{
			{Key: configyaml.DemoAPIKey, Value: d.demoHTTP.API},
			{Key: configyaml.DemoPostgresKey, Value: d.demoPG.API},
		}
	}
	// An mcp lane refuses an analyzer, so the hint would lead to an invalid config.
	if !d.an.on && d.protocol != "mcp" {
		var lanes []laneHint
		for _, l := range ls {
			trigger := d.an.trigger
			if d.isDemo() {
				trigger = demoTriggers[l.Protocol]
			}
			lanes = append(lanes, laneHint{l.Name, trigger})
		}
		o.Footer = analyzerHint(d.an, lanes)
	}
	return o
}

// laneHint is one listener's analyzer block in analyzerHint.
type laneHint struct {
	name    string
	trigger []string
}

// analyzerHint is the analyzer, commented out, for a config written with it
// off: one uncomment away once its key exists.
func analyzerHint(a analyzerDraft, lanes []laneHint) []string {
	out := []string{"AI analyzer, off. A model rates each statement's risk and catches what no",
		"rule can express, like a DELETE with no WHERE clause. To turn it on:", ""}
	if env, ok := keyEnv[a.provider]; ok {
		out = append(out, fmt.Sprintf("  mkdir -p %s && (umask 077; printf %%s \"$%s\" > %s)", a.keyDir, env, a.keyFile), "")
	}
	names := make([]string, len(lanes))
	for i, l := range lanes {
		names[i] = l.name
	}
	out = append(out, "then add this block, and the lane block to listener "+strings.Join(names, " and ")+":", "", "analyzer:")
	switch a.provider {
	case hostedProvider:
		out = append(out, "  use_hoop_llm_provider: true  # "+hostedPIINote)
	case "vertex":
		out = append(out, "  provider: "+a.provider, "  model: "+a.model,
			"  extra: {project: my-gcp-project, region: global}")
	default:
		out = append(out, "  provider: "+a.provider, "  model: "+a.model)
		out = append(out, "  credentials_file: "+a.keyFile)
	}
	for _, l := range lanes {
		if len(lanes) > 1 {
			out = append(out, "", "    # "+l.name)
		} else {
			out = append(out, "")
		}
		out = append(out, "    analyzer:", "      trigger: {operations: ["+strings.Join(l.trigger, ", ")+"]}",
			"      high: "+a.high, "      medium: "+a.medium, "      low: "+a.low)
	}
	return out
}

// ---- summaries for the overview ---------------------------------------------

func (d *draft) rulesSummary() string {
	if len(d.rules) == 0 {
		return "none"
	}
	s := fmt.Sprintf("%d rule%s · %s", len(d.rules), plural(len(d.rules)), d.rules[0].Name)
	if d.guardMode == "observe" {
		s += " · observe only"
	}
	return s
}

func (d *draft) masksSummary() string {
	if len(d.masks) == 0 {
		return "none"
	}
	r := d.masks[0]
	what := strings.Join(r.Entities, ", ")
	if len(r.Columns) > 0 {
		what = "columns " + strings.Join(r.Columns, ", ")
	}
	strategy := string(r.Strategy)
	if strategy == "" {
		strategy = "redact"
	}
	return fmt.Sprintf("%d rule%s · %s → %s", len(d.masks), plural(len(d.masks)), what, strategy)
}

func (d *draft) analyzerSummary() string {
	if !d.an.on {
		if d.protocol == "mcp" {
			return "off · an mcp listener does not take one yet"
		}
		if needsKey(d.an.provider) && keyFileStatus(d.an.keyFile) != nil {
			return "off · needs a key file for " + d.an.provider
		}
		return "off"
	}
	if d.an.provider == hostedProvider {
		return fmt.Sprintf("Hoop-hosted %s · sent to Hoop · high→%s, medium→%s, low→%s",
			analyzer.HostedModel, d.an.high, d.an.medium, d.an.low)
	}
	return fmt.Sprintf("%s %s · high→%s, medium→%s, low→%s", d.an.provider, d.an.model, d.an.high, d.an.medium, d.an.low)
}

func (d *draft) piiSummary() string {
	if len(d.pii.entities) == 0 {
		s := "every type it knows"
		if len(d.pii.ignored) > 0 {
			s += fmt.Sprintf(" but %d ignored", len(d.pii.ignored))
		}
		return s
	}
	return fmt.Sprintf("%d type%s", len(d.pii.entities), plural(len(d.pii.entities)))
}

// ruleDetail is a rule's match in a few words, for the rule list.
func ruleDetail(r policy.Rule) string {
	switch r.Type {
	case policy.MatchDenyWords:
		return strings.Join(r.Words, ", ")
	case policy.MatchPattern:
		return r.Pattern
	case policy.MatchOperation:
		return strings.Join(opStrings(r.Operations), ", ")
	case policy.MatchTable:
		return strings.Join(r.Tables, ", ")
	case policy.MatchProcedure:
		return strings.Join(r.Procedures, ", ")
	case policy.MatchPII:
		return strings.Join(r.Entities, ", ")
	case policy.MatchHTTPResource:
		return strings.Join(r.Methods, ",") + " " + strings.Join(r.Resources, ", ")
	case policy.MatchHTTPStatus, policy.MatchGRPCStatus:
		return strings.Join(r.Statuses, ", ")
	case policy.MatchHTTPHeader:
		return headerText(r.Headers)
	case policy.MatchMetadata:
		return headerText(r.Metadata)
	case policy.MatchMCPTool:
		if len(r.Allow) > 0 {
			return "only " + strings.Join(r.Allow, ", ")
		}
		return strings.Join(r.Tools, ", ")
	}
	return ""
}

func maskDetail(r alcatraz.Rule) string {
	what := strings.Join(r.Entities, ", ")
	if len(r.Columns) > 0 {
		what = "columns " + strings.Join(r.Columns, ", ")
	}
	s := string(r.Strategy)
	if s == "" {
		s = "redact"
	}
	return what + " → " + s
}

func newMask(n int) alcatraz.Rule {
	return alcatraz.Rule{Name: fmt.Sprintf("mask-%d", n+1), Strategy: alcatraz.StrategyRedact}
}

// ---- guardrail rule form ----------------------------------------------------

// captures reports whether the listener decodes gRPC payloads, which
// operation, table and pii rules on a gRPC lane need.
func (d *draft) captures() bool {
	if d.listener == nil {
		return false
	}
	f := d.listener.form.byID("grpc.capture_payload")
	return f != nil && f.on
}

func ruleForm(r policy.Rule, protocol string, captures, isNew bool) *form {
	types := ruleTypesFor(protocol, captures)
	typ := &field{id: "type", label: "Type", kind: fEnum, options: types, optLabel: ruleTypeLabel, text: string(r.Type),
		help: "What the rule looks at. ‹ › changes it."}
	if !slices.Contains(types, typ.text) {
		typ.text = types[0]
	}
	is := func(t ...policy.MatchType) func() bool {
		return func() bool { return !slices.Contains(t, policy.MatchType(typ.text)) }
	}
	entities := alcatraz.AllEntities()
	fields := []*field{
		typ,
		{id: "name", label: "Name", kind: fText, text: r.Name, placeholder: "no-drops",
			help: "Appears in the audit trail and in the denial the client gets."},
		{id: "words", label: "Words", kind: fText, text: strings.Join(r.Words, ", "), placeholder: "drop table, rm -rf",
			help: "A statement containing any of these words is matched. Comma-separated.", hidden: is(policy.MatchDenyWords)},
		{id: "pattern", label: "Regex", kind: fText, text: r.Pattern, placeholder: `(?i)\bpassword\b`,
			help: "An RE2 regular expression over the statement text.", hidden: is(policy.MatchPattern)},
		{id: "operations", label: "Operations", kind: fMulti, multi: opStrings(r.Operations), options: opsFor(protocol),
			placeholder: "pick at least one", help: "Read from the parsed statement, not the text.", hidden: is(policy.MatchOperation)},
		{id: "tables", label: "Tables", kind: fText, text: strings.Join(r.Tables, ", "), placeholder: "customers, payments",
			help: "Statements that touch these tables. Comma-separated.", hidden: is(policy.MatchTable)},
		{id: "access", label: "Access", kind: fEnum, options: []string{"", "read", "write"}, text: r.Access,
			optLabel: map[string]string{"": "read and write"}, help: "Match only reads, only writes, or both.", hidden: is(policy.MatchTable)},
		{id: "require", label: "Require table match", kind: fBool, on: r.RequireTableMatch,
			help: "Also match statements whose tables could not be read.", hidden: is(policy.MatchTable)},
		{id: "procedures", label: "Procedures", kind: fText, text: strings.Join(r.Procedures, ", "), placeholder: "get_secrets, dbo.delete_customer",
			help: "Stored procedures called by name, in any spelling (dbo.x, [dbo].[x], appdb.dbo.x). Comma-separated.", hidden: is(policy.MatchProcedure)},
		{id: "entities", label: "Data types", kind: fMulti, multi: r.Entities, options: entities, placeholder: "pick at least one",
			help: "A statement carrying one of these is matched.", hidden: is(policy.MatchPII)},
		{id: "resources", label: "Resources", kind: fText, text: strings.Join(r.Resources, ", "), placeholder: "/admin/*, /users/{id}",
			help: "Request paths. Comma-separated.", hidden: is(policy.MatchHTTPResource)},
		{id: "methods", label: "Methods", kind: fMulti, multi: r.Methods,
			options: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}, placeholder: "any",
			help: "Limit the match to these methods.", hidden: is(policy.MatchHTTPResource)},
		{id: "statuses", label: "Statuses", kind: fText, text: strings.Join(r.Statuses, ", "), placeholder: "5xx, 403",
			help: "Codes (404) or classes (4xx). Comma-separated.", hidden: is(policy.MatchHTTPStatus, policy.MatchGRPCStatus)},
		{id: "headers", label: "Headers", kind: fText, text: headerText(r.Headers), placeholder: "x-env=prod|staging",
			help: "Match when a header has one of these values. name=v1|v2, comma-separated.", hidden: is(policy.MatchHTTPHeader)},
		{id: "headers_not", label: "Headers not", kind: fText, text: headerText(r.HeadersNot), placeholder: "x-team=ops",
			help: "Match when a header does NOT have one of these values.", hidden: is(policy.MatchHTTPHeader)},
		{id: "metadata", label: "Metadata", kind: fText, text: headerText(r.Metadata), placeholder: "grpc.method=DeleteUser|DropTable",
			help:   "Match when every key has one of its values; keys come from the codec (grpc.*, mongodb.command, <plug-in>.verb). key=v1|v2, comma-separated.",
			hidden: is(policy.MatchMetadata)},
		{id: "tools", label: "Deny tools", kind: fText, text: strings.Join(r.Tools, ", "), placeholder: "*delete*, run_query",
			help: "Tools the rule denies and hides from tools/list. * matches anything; comma-separated.", hidden: is(policy.MatchMCPTool)},
		{id: "allow", label: "Allow only", kind: fText, text: strings.Join(r.Allow, ", "), placeholder: "get_*, list_*",
			help: "The only tools the rule lets through. Set this or Deny tools, not both.", hidden: is(policy.MatchMCPTool)},
		{id: "message", label: "Message", kind: fText, text: r.Message, placeholder: "not allowed through the sidecar",
			help: "What the client is told when the rule refuses a statement."},
		{id: "action", label: "On match", kind: fEnum, options: []string{"", "defer"}, text: r.Action,
			optLabel: map[string]string{"": "refuse", "defer": "report, let the next evaluator decide"}},
		{id: "done", label: "Done", kind: fButton},
	}
	if !isNew {
		fields = append(fields, &field{id: "delete", label: "Delete this rule", kind: fButton})
	}
	return newForm("Guardrail rule", fields...)
}

func headerText(m map[string][]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+"="+strings.Join(m[k], "|"))
	}
	return strings.Join(parts, ", ")
}

func parseHeaders(f *field) (map[string][]string, error) {
	if strings.TrimSpace(f.text) == "" {
		return nil, nil
	}
	out := map[string][]string{}
	for _, kv := range f.list() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("%s: %q is not name=value", f.label, kv)
		}
		for _, x := range strings.Split(v, "|") {
			if x = strings.TrimSpace(x); x != "" {
				out[strings.TrimSpace(k)] = append(out[strings.TrimSpace(k)], x)
			}
		}
	}
	return out, nil
}

// ruleFromForm reads a rule back, refusing one that names nothing to match
// on, which policy would refuse at startup anyway; saying so here keeps the
// person on the field to fix.
func ruleFromForm(f *form) (policy.Rule, error) {
	f.store()
	g := func(id string) *field { return f.byID(id) }
	r := policy.Rule{Name: strings.TrimSpace(g("name").text), Type: policy.MatchType(g("type").text),
		Message: strings.TrimSpace(g("message").text), Action: g("action").text}
	if r.Name == "" {
		return r, errors.New("give the rule a name")
	}
	var empty string
	switch r.Type {
	case policy.MatchDenyWords:
		if r.Words = g("words").list(); len(r.Words) == 0 {
			empty = "Words"
		}
	case policy.MatchPattern:
		if r.Pattern = strings.TrimSpace(g("pattern").text); r.Pattern == "" {
			empty = "Regex"
		}
	case policy.MatchOperation:
		if r.Operations = ops(g("operations").multi...); len(r.Operations) == 0 {
			empty = "Operations"
		}
	case policy.MatchTable:
		r.Access, r.RequireTableMatch = g("access").text, g("require").on
		if r.Tables = g("tables").list(); len(r.Tables) == 0 {
			empty = "Tables"
		}
	case policy.MatchProcedure:
		if r.Procedures = g("procedures").list(); len(r.Procedures) == 0 {
			empty = "Procedures"
		}
	case policy.MatchPII:
		if r.Entities = g("entities").multi; len(r.Entities) == 0 {
			empty = "Data types"
		}
	case policy.MatchHTTPResource:
		r.Methods = g("methods").multi
		if r.Resources = g("resources").list(); len(r.Resources) == 0 {
			empty = "Resources"
		}
	case policy.MatchHTTPStatus, policy.MatchGRPCStatus:
		if r.Statuses = g("statuses").list(); len(r.Statuses) == 0 {
			empty = "Statuses"
		}
	case policy.MatchHTTPHeader:
		var err error
		if r.Headers, err = parseHeaders(g("headers")); err != nil {
			return r, err
		}
		if r.HeadersNot, err = parseHeaders(g("headers_not")); err != nil {
			return r, err
		}
		if len(r.Headers) == 0 && len(r.HeadersNot) == 0 {
			empty = "Headers or Headers not"
		}
	case policy.MatchMetadata:
		var err error
		if r.Metadata, err = parseHeaders(g("metadata")); err != nil {
			return r, err
		}
		if len(r.Metadata) == 0 {
			empty = "Metadata"
		}
	case policy.MatchMCPTool:
		r.Tools, r.Allow = g("tools").list(), g("allow").list()
		if len(r.Tools) > 0 && len(r.Allow) > 0 {
			return r, errors.New("set Deny tools or Allow only, not both")
		}
		if len(r.Tools) == 0 && len(r.Allow) == 0 {
			empty = "Deny tools"
		}
	}
	if empty != "" {
		return r, fmt.Errorf("%s is empty: a %s rule needs it", empty, ruleTypeLabel[string(r.Type)])
	}
	return r, nil
}

// ---- masking rule form ------------------------------------------------------

func maskForm(r alcatraz.Rule, isNew bool) *form {
	by := "entities"
	if len(r.Columns) > 0 {
		by = "columns"
	}
	byF := &field{id: "by", label: "Find values by", kind: fEnum, options: []string{"entities", "columns"}, text: by,
		optLabel: map[string]string{"entities": "what they look like", "columns": "where they are"}}
	strategy := string(r.Strategy)
	if strategy == "" {
		strategy = string(alcatraz.StrategyRedact)
	}
	st := &field{id: "strategy", label: "Strategy", kind: fEnum, text: strategy,
		options: []string{"redact", "mask", "partial", "hash"},
		optLabel: map[string]string{"redact": "redact: [REDACTED:TYPE]", "mask": "mask: every character",
			"partial": "partial: keep the last few", "hash": "hash: same input, same token"}}
	keep := ""
	if r.KeepLast > 0 {
		keep = strconv.Itoa(r.KeepLast)
	}
	char := ""
	if r.MaskChar != 0 {
		char = string(r.MaskChar)
	}
	fields := []*field{
		{id: "name", label: "Name", kind: fText, text: r.Name, placeholder: "emails"},
		byF,
		{id: "entities", label: "Data types", kind: fMulti, multi: r.Entities, options: alcatraz.AllEntities(),
			placeholder: "pick at least one", help: "One rule may name several; the free tier allows one rule.",
			hidden: func() bool { return byF.text != "entities" }},
		{id: "columns", label: "Columns", kind: fText, text: strings.Join(r.Columns, ", "), placeholder: "email, ssn",
			help:   "Column names, or JSON key paths on an HTTP lane. Comma-separated.",
			hidden: func() bool { return byF.text != "columns" }},
		st,
		{id: "keep", label: "Keep last", kind: fInt, text: keep, placeholder: "4",
			hidden: func() bool { return st.text != "partial" }},
		{id: "char", label: "Mask character", kind: fText, text: char, placeholder: "*",
			hidden: func() bool { return st.text != "mask" && st.text != "partial" }},
		{id: "done", label: "Done", kind: fButton},
	}
	if !isNew {
		fields = append(fields, &field{id: "delete", label: "Delete this rule", kind: fButton})
	}
	return newForm("Masking rule", fields...)
}

func maskFromForm(f *form) (alcatraz.Rule, error) {
	f.store()
	g := func(id string) *field { return f.byID(id) }
	r := alcatraz.Rule{Name: strings.TrimSpace(g("name").text), Strategy: alcatraz.Strategy(g("strategy").text)}
	if r.Name == "" {
		return r, errors.New("give the rule a name")
	}
	if g("by").text == "columns" {
		if r.Columns = g("columns").list(); len(r.Columns) == 0 {
			return r, errors.New("Columns is empty")
		}
	} else if r.Entities = g("entities").multi; len(r.Entities) == 0 {
		return r, errors.New("pick at least one data type")
	}
	if r.Strategy == alcatraz.StrategyPartial {
		if t := strings.TrimSpace(g("keep").text); t != "" {
			n, err := strconv.Atoi(t)
			if err != nil || n < 0 {
				return r, fmt.Errorf("Keep last: %q is not a whole number", t)
			}
			r.KeepLast = n
		}
	}
	if r.Strategy == alcatraz.StrategyMask || r.Strategy == alcatraz.StrategyPartial {
		if c := []rune(g("char").text); len(c) > 1 {
			return r, errors.New("Mask character: one character")
		} else if len(c) == 1 {
			r.MaskChar = c[0]
		}
	}
	return r, nil
}

// ---- analyzer form ----------------------------------------------------------

var riskActions = []string{
	string(analyzer.ActionAllow), string(analyzer.ActionWarn), string(analyzer.ActionBlock),
	string(analyzer.ActionDefer), string(analyzer.ActionRequireReview),
}

var riskActionLabel = map[string]string{
	"allow": "allow", "warn": "allow, flag in the audit", "block": "block",
	"defer": "report, let policy decide", "require_review": "hold for approval in this terminal",
}

func analyzerForm(a analyzerDraft, protocol string) *form {
	providers := analyzerProviders()
	prov := &field{id: "provider", label: "Provider", kind: fEnum, options: providers, optLabel: providerLabel, text: a.provider}
	if !slices.Contains(providers, prov.text) && len(providers) > 0 {
		prov.text = providers[0]
	}
	// Every field shows while the analyzer is off too: what turning it on
	// takes (a key file, a model) is the thing the person is deciding on.
	hideForVertex := func() bool { return !needsKey(prov.text) }
	hideUnlessVertex := func() bool { return prov.text != "vertex" }
	hideForHosted := func() bool { return prov.text == hostedProvider }
	hideUnlessHosted := func() bool { return prov.text != hostedProvider }
	key := &field{id: "key", label: "Key file", kind: fText, text: a.keyFile, hidden: hideForVertex,
		help: "Read as written: no ~. Must be mode 0600 or stricter."}
	fields := []*field{
		{id: "on", label: "Enabled", kind: fBool, on: a.on,
			help: "Off writes the analyzer into the file commented out, one uncomment away."},
		prov,
		{id: "hostednote", kind: fNote, hidden: hideUnlessHosted, note: func() string {
			return stPrimary.Render("✓ no key needed · runs "+analyzer.HostedModel+", hosted by Hoop") +
				"\n" + stFaint.Render("  "+hostedPIINote)
		}},
		{id: "model", label: "Model", kind: fText, text: a.model, placeholder: starterModels[prov.text], hidden: hideForHosted,
			help: "Any model the provider serves to your account."},
		key,
		{id: "keynote", kind: fNote, hidden: hideForVertex, note: func() string {
			path := key.text
			if err := keyFileStatus(path); err != nil {
				s := stDanger.Render("✕ key file " + err.Error())
				if env, ok := keyEnv[prov.text]; ok {
					s += "\n" + stFaint.Render(fmt.Sprintf("  create it: mkdir -p %s && (umask 077; printf %%s \"$%s\" > %s)", filepath.Dir(path), env, path))
				}
				return s
			}
			return stPrimary.Render("✓ key file found, mode 0600")
		}},
		{id: "project", label: "GCP project", kind: fText, text: a.project, placeholder: "my-gcp-project", hidden: hideUnlessVertex,
			help: "Vertex uses Google Application Default Credentials."},
		{id: "region", label: "Region", kind: fText, text: a.region, placeholder: "global", hidden: hideUnlessVertex},
		{id: "trigger", label: "Look at", kind: fMulti, multi: a.trigger, options: opsFor(protocol), placeholder: "every statement",
			hidden: func() bool { return protocol == demoProtocol },
			help:   "Which operations are sent to the model. Empty sends everything."},
		{id: "demotrigger", kind: fNote, hidden: func() bool { return protocol != demoProtocol }, note: func() string {
			return stFaint.Render("Each demo listener sends its writes: " + demoHTTPListener + " " +
				strings.Join(demoTriggers["http"], ", ") + "; " + demoPGListener + " " + strings.Join(demoTriggers["postgres"], ", ") + ".")
		}},
		{id: "high", label: "High risk", kind: fEnum, options: riskActions, optLabel: riskActionLabel, text: a.high},
		{id: "medium", label: "Medium risk", kind: fEnum, options: riskActions, optLabel: riskActionLabel, text: a.medium},
		{id: "low", label: "Low risk", kind: fEnum, options: riskActions, optLabel: riskActionLabel, text: a.low},
		{id: "done", label: "Done", kind: fButton},
	}
	return newForm("AI analyzer", fields...)
}

func analyzerFromForm(f *form, a analyzerDraft) (analyzerDraft, error) {
	f.store()
	g := func(id string) *field { return f.byID(id) }
	prev := a.provider
	a.on, a.provider = g("on").on, g("provider").text
	a.model = strings.TrimSpace(g("model").text)
	if a.provider != prev && (a.model == "" || a.model == starterModels[prev]) {
		a.model = starterModels[a.provider]
	}
	a.keyFile = strings.TrimSpace(g("key").text)
	// A suggested path follows the provider; a path the person typed stays.
	if a.provider != prev && needsKey(a.provider) && filepath.Dir(a.keyFile) == a.keyDir &&
		strings.HasSuffix(a.keyFile, ".key") && starterModels[strings.TrimSuffix(filepath.Base(a.keyFile), ".key")] != "" {
		a.keyFile = filepath.Join(a.keyDir, a.provider+".key")
	}
	a.project, a.region = strings.TrimSpace(g("project").text), strings.TrimSpace(g("region").text)
	a.trigger = g("trigger").multi
	a.high, a.medium, a.low = g("high").text, g("medium").text, g("low").text
	if !a.on {
		return a, nil
	}
	if a.provider == hostedProvider {
		return a, nil
	}
	if a.model == "" {
		return a, errors.New("Model is empty")
	}
	if a.provider == "vertex" && a.project == "" {
		return a, errors.New("GCP project is empty")
	}
	if a.provider != "vertex" && a.keyFile == "" {
		return a, errors.New("Key file is empty")
	}
	return a, nil
}

// ---- pii form ---------------------------------------------------------------

func piiForm(p piiDraft) *form {
	all := alcatraz.AllEntities()
	return newForm("Sensitive data detection",
		&field{id: "entities", label: "Detect", kind: fMulti, multi: p.entities, options: all, placeholder: "every type",
			help: "Narrows detection to these types. Empty detects all of them."},
		&field{id: "ignored", label: "Ignore", kind: fMulti, multi: p.ignored, options: all, placeholder: "none",
			help: "Types that fire on ordinary data in your traffic (DATE_TIME, URL...)."},
		&field{id: "threshold", label: "Threshold", kind: fText, text: p.threshold, placeholder: "default",
			help: "Minimum confidence, 0 to 1."},
		&field{id: "allow", label: "Allow list", kind: fText, text: strings.Join(p.allow, ", "), placeholder: "none",
			help: "Values never treated as sensitive. Comma-separated."},
		&field{id: "language", label: "Language", kind: fText, text: p.language, placeholder: "en"},
		&field{id: "done", label: "Done", kind: fButton},
	)
}

func piiFromForm(f *form) (piiDraft, error) {
	f.store()
	g := func(id string) *field { return f.byID(id) }
	p := piiDraft{entities: g("entities").multi, ignored: g("ignored").multi, allow: g("allow").list(),
		threshold: strings.TrimSpace(g("threshold").text), language: strings.TrimSpace(g("language").text)}
	if p.threshold != "" {
		if t, err := strconv.ParseFloat(p.threshold, 64); err != nil || t < 0 || t > 1 {
			return p, fmt.Errorf("Threshold %q: want a number from 0 to 1", p.threshold)
		}
	}
	return p, nil
}
