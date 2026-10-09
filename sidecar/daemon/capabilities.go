package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/policy"
)

// CapabilitiesHeader lists, comma separated, what this build decodes of the
// served document, and the behaviours it has toward the plane. The sidecar
// sends it on every handshake.
//
// A header, like the answer's LicenseManagedHeader and ConfigRevisionHeader.
// Absent means a build too old to report; the plane keeps that apart from a
// build that reports none.
//
// The served document is decoded with DisallowUnknownFields, so a key this
// build does not declare fails the whole document, and a rule type or a
// protocol it does not know fails Validate. The plane reads this header to
// refuse such a document at save and at serve instead; see CheckServable.
const CapabilitiesHeader = "hoop-sidecar-capabilities"

// Four kinds of entry travel in the header. SidecarCapabilities generates
// the first three from what the build links, so a new field, rule type or
// protocol is reported without anyone listing it (ADR-0022):
//
//   - A bare name is a FIELD: the cap:"<name>" tag on the struct field that
//     decodes it. A document that sets the field needs the entry. Every
//     new field needs the tag, unless it ships in the same release as a
//     tagged block around it: the plane serves no zero value, but an
//     older build refuses a key it does not declare once an admin sets it
//     (EVL-338). A since:"<release>" tag beside it names the release the
//     field shipped in; see CheckServable for what it decides.
//   - rule:<type> is a guardrail rule type policy.RuleTypes lists.
//   - protocol:<name> is a listener protocol Protocols lists.
//   - A bare name no field carries is a BEHAVIOUR this build has toward the
//     plane, listed in behaviourCapabilities. CheckServable never reads one:
//     it says what the sidecar does, not what it decodes.
const (
	// CapabilityReviewMode means this build decodes an analyzer block's
	// review_mode.
	CapabilityReviewMode = "review_mode"
	// CapabilityApprovalMode means this build decodes an analyzer block's
	// approval_mode, the alias of review_mode. ServedForm serves the
	// canonical key, so a plane refuses it only when the two disagree.
	CapabilityApprovalMode = "approval_mode"
	// CapabilityAnalyzerRateLimit means this build decodes analyzer
	// rate_limit, on the top-level section and on a listener's block.
	CapabilityAnalyzerRateLimit = "analyzer_rate_limit"
	// CapabilityAnalyzerTriggerItems means this build decodes an analyzer
	// trigger's any and exclude, on the block and on the rule form.
	CapabilityAnalyzerTriggerItems = "analyzer_trigger_items"
	// CapabilitySessionEvents means this build sends its audit events to
	// the plane when the handshake answers with SessionEventsHeader.
	CapabilitySessionEvents = "session_events"

	capabilityRulePrefix     = "rule:"
	capabilityProtocolPrefix = "protocol:"
)

// baselineCapabilities is the rule and protocol vocabulary of the builds
// whose header carries no rule: or protocol: entry, each with the release
// that added it when that is later than 1.162.0, the first release that
// handshakes with a control plane. A handshake that names none of a kind
// comes from such a build, and CheckServable grants it an entry from the
// release it shipped in; an empty release means every such build has it.
//
// FROZEN. An entry added here is granted to old builds by release alone, and
// a release that never shipped it would refuse it. A new rule type or
// protocol reaches the header through policy.RuleTypes and Protocols alone.
var baselineCapabilities = map[string]string{
	"protocol:clickhouse":  "1.183.0",
	"protocol:grpc":        "",
	"protocol:http":        "",
	"protocol:mongodb":     "",
	"protocol:mssql":       "",
	"protocol:mysql":       "",
	"protocol:postgres":    "",
	"protocol:spanner":     "1.166.0",
	"protocol:ssh":         "1.176.0",
	"rule:ai_analysis":     "",
	"rule:deny_words_list": "",
	"rule:grpc_status":     "",
	"rule:http_header":     "1.194.0",
	"rule:http_resource":   "",
	"rule:http_status":     "",
	"rule:operation":       "",
	"rule:pattern_match":   "",
	"rule:pii":             "",
	"rule:table":           "",
}

// Handshake is what a sidecar said about itself, as far as serving needs it.
type Handshake struct {
	// Version is the release the sidecar reported. It grants a since-tagged
	// field, and a baseline entry to a build whose header predates the
	// generated list; see CheckServable.
	Version string
	// Capabilities is the parsed CapabilitiesHeader. nil means the sidecar
	// never reported one.
	Capabilities []string
}

// capField is one cap-tagged field: where it sits, what the header calls it,
// and the release it shipped in when the tag says.
type capField struct {
	path, name, since string
}

// capFields lists every cap-tagged field of the config, in walk order.
func capFields() []capField {
	var out []capField
	walkConfigType(reflect.TypeFor[Config](), "", func(path string, f jsonField) {
		if c := f.tag.Get("cap"); c != "" {
			out = append(out, capField{path: path, name: c, since: f.tag.Get("since")})
		}
	})
	return out
}

// capabilitySince maps an entry to the release it shipped in: every field
// whose tag says, read from the struct so the number sits beside the field
// it describes, and every baseline entry newer than the first handshaking
// release.
func capabilitySince() map[string]string {
	out := map[string]string{}
	for _, f := range capFields() {
		if f.since != "" {
			out[f.name] = f.since
		}
	}
	for c, release := range baselineCapabilities {
		if release != "" {
			out[c] = release
		}
	}
	return out
}

// behaviourCapabilities are the entries no field, rule type or protocol
// generates. A behaviour has no type to generate it from, so it is listed.
var behaviourCapabilities = []string{CapabilitySessionEvents}

// SidecarCapabilities is what this build sends in CapabilitiesHeader: every
// cap-tagged field of the config, every rule type and every protocol it
// links, and every behaviour it has. Sorted, so two builds that decode the
// same document send the same header.
func SidecarCapabilities() []string {
	var out []string
	for _, f := range capFields() {
		out = append(out, f.name)
	}
	out = append(out, behaviourCapabilities...)
	for _, t := range policy.RuleTypes() {
		out = append(out, capabilityRulePrefix+string(t))
	}
	for _, p := range Protocols() {
		out = append(out, capabilityProtocolPrefix+p)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ParseCapabilities reads a CapabilitiesHeader value. It never returns nil,
// so a caller can store "reported none" apart from "never reported".
func ParseCapabilities(header string) []string {
	out := []string{}
	for _, c := range strings.Split(header, ",") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// servedView is the document as the sidecar decodes it: ServedForm, through
// the JSON the plane sends. A value omitempty drops, an empty list or map, is
// absent on the wire, so it is absent here and needs no entry.
func servedView(cfg Config) (Config, error) {
	raw, err := json.Marshal(ServedForm(cfg))
	if err != nil {
		return Config{}, fmt.Errorf("encode the configuration: %w", err)
	}
	var out Config
	if err := json.Unmarshal(raw, &out); err != nil {
		return Config{}, fmt.Errorf("decode the configuration: %w", err)
	}
	return out, nil
}

// CheckServable refuses a document that the sidecar behind hs cannot decode:
// a set field whose entry it lacks, a rule of a type it does not list, or a
// listener protocol it does not speak. It reads the document as the sidecar
// will, so a default the plane never serves and a value the wire drops need
// no entry. The error names the listener and the release that adds support.
//
// A since-tagged field is granted to any build whose reported release
// parses and reaches the release the field shipped in, because a build can
// decode a field it does not report: the tag may postdate the field. A
// build from before the generated list names no rule type or protocol, so
// for such a build the release also grants a baseline entry. A build that
// sends the list is read from it alone for rule types and protocols. A
// release that does not parse (a dev build's "unknown") grants nothing by
// release.
func CheckServable(cfg Config, hs Handshake) error {
	served, err := servedView(cfg)
	if err != nil {
		return err
	}
	since := capabilitySince()
	granted := func(capability string) bool { return hs.grants(capability, since) }
	who := "this sidecar"
	if hs.Version != "" {
		who = fmt.Sprintf("this sidecar (%s)", hs.Version)
	}
	var problems []string
	need := func(where, what, capability string) {
		if granted(capability) {
			return
		}
		problems = append(problems, fmt.Sprintf("%s %s, and %s does not support it; %s",
			where, what, who, upgradeHint(capability, since)))
	}
	walkConfigValue(reflect.ValueOf(served), "the configuration", "",
		func(where, path string, f jsonField, v reflect.Value) {
			if c := f.tag.Get("cap"); c != "" && !v.IsZero() {
				need(where, "sets "+path, c)
			}
		})
	forEachRuleSet(served, func(where string, rules []policy.Rule) {
		for _, r := range rules {
			need(where, fmt.Sprintf("binds the %s rule %q", r.Type, r.Name), capabilityRulePrefix+string(r.Type))
		}
	})
	for _, l := range served.Listeners {
		if l.Protocol != "" {
			need(fmt.Sprintf("listener %q", l.Name), "speaks "+l.Protocol, capabilityProtocolPrefix+l.Protocol)
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// grants reports whether the build behind hs decodes capability, an entry
// of the header; since is capabilitySince. CheckServable refuses what it
// does not grant.
func (hs Handshake) grants(capability string, since map[string]string) bool {
	if slices.Contains(hs.Capabilities, capability) {
		return true
	}
	release, releaseKnown := parseRelease(hs.Version)
	shipped, shippedKnown := parseRelease(since[capability])
	reaches := releaseKnown && shippedKnown && !releaseBefore(release, shipped)
	// A field is granted by its since release to every build, list or not.
	// A field is never removed, and the releases that shipped one before it
	// carried a cap tag decode it without reporting it: 1.210 and 1.211 list
	// no trust (EVL-338).
	kind, isEntry := capabilityRulePrefix, strings.HasPrefix(capability, capabilityRulePrefix)
	if strings.HasPrefix(capability, capabilityProtocolPrefix) {
		kind, isEntry = capabilityProtocolPrefix, true
	}
	if !isEntry {
		return reaches
	}
	// A header that names no entry of a kind comes from a build older than
	// that kind's generated list.
	if slices.ContainsFunc(hs.Capabilities, func(c string) bool { return strings.HasPrefix(c, kind) }) {
		return false
	}
	if shipped, inBaseline := baselineCapabilities[capability]; inBaseline && shipped == "" {
		return true
	}
	return reaches
}

func upgradeHint(capability string, since map[string]string) string {
	if s, ok := since[capability]; ok {
		return fmt.Sprintf("upgrade the sidecar to %s or later", s)
	}
	return fmt.Sprintf("upgrade the sidecar to a release that reports %q", capability)
}

// parseRelease reads a hoop release, MAJOR.MINOR.PATCH and nothing else.
// "unknown", a preview tag and anything with a suffix do not parse, and a
// caller then decides nothing from the version.
func parseRelease(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p != strconv.Itoa(n) {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func releaseBefore(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// forEachRuleSet visits every guardrail rule list of a document, the
// deprecated policy spelling included: the plane can hold a document written
// before the rename, and the sidecar folds it only after decoding it.
func forEachRuleSet(cfg Config, visit func(where string, rules []policy.Rule)) {
	if cfg.Guardrails != nil {
		visit("the configuration", cfg.Guardrails.Rules)
	}
	if cfg.Policy != nil {
		visit("the configuration", cfg.Policy.Rules)
	}
	for _, l := range cfg.Listeners {
		where := fmt.Sprintf("listener %q", l.Name)
		if l.Guardrails != nil {
			visit(where, l.Guardrails.Rules)
		}
		if l.Policy != nil {
			visit(where, l.Policy.Rules)
		}
	}
}

// ServedForm drops the review_mode key where it holds the default, so a hold
// lane serves the same document an older build decodes. It copies what it
// changes: cfg may share listeners with a stored row.
//
// The one hand-written default: "hold" is a value, and every other default is
// the zero value, which the field's own tag omits.
//
// It also serves the approval spellings an admin may have written in their
// canonical form: approval_mode as review_mode, and require_approval as
// require_review on every risk level. An older build decodes neither alias
// and refuses the whole document over one, and the canonical form is what
// every build reads. approval_mode that disagrees with review_mode is served
// as written: normalize refuses it, and picking one here would hide that.
func ServedForm(cfg Config) Config {
	listeners := make([]ListenerConfig, len(cfg.Listeners))
	copy(listeners, cfg.Listeners)
	for i, l := range listeners {
		listeners[i].Guardrails = servedGuardrails(l.Guardrails)
		listeners[i].Policy = servedPolicy(l.Policy)
		if l.Analyzer == nil {
			continue
		}
		block := *l.Analyzer
		if block.ApprovalMode != "" && (block.ReviewMode == "" || block.ReviewMode == block.ApprovalMode) {
			block.ReviewMode, block.ApprovalMode = block.ApprovalMode, ""
		}
		if block.ReviewMode == "hold" {
			block.ReviewMode = ""
		}
		block.HighRisk = canonicalAction(block.HighRisk)
		block.MediumRisk = canonicalAction(block.MediumRisk)
		block.LowRisk = canonicalAction(block.LowRisk)
		if !reflect.DeepEqual(block, *l.Analyzer) {
			listeners[i].Analyzer = &block
		}
	}
	cfg.Listeners = listeners
	cfg.Guardrails = servedGuardrails(cfg.Guardrails)
	cfg.Policy = servedPolicy(cfg.Policy)
	return cfg
}

// servedRules returns rules with require_approval served as require_review on
// the deprecated ai_analysis rule form. It returns rules itself when nothing
// changes, and a copy otherwise.
func servedRules(rules []policy.Rule) ([]policy.Rule, bool) {
	var out []policy.Rule
	for i, r := range rules {
		high, medium, low := canonicalAction(r.HighRisk), canonicalAction(r.MediumRisk), canonicalAction(r.LowRisk)
		if high == r.HighRisk && medium == r.MediumRisk && low == r.LowRisk {
			continue
		}
		if out == nil {
			out = slices.Clone(rules)
		}
		out[i].HighRisk, out[i].MediumRisk, out[i].LowRisk = high, medium, low
	}
	if out == nil {
		return rules, false
	}
	return out, true
}

func servedGuardrails(g *GuardrailsConfig) *GuardrailsConfig {
	if g == nil {
		return nil
	}
	rules, changed := servedRules(g.Rules)
	if !changed {
		return g
	}
	out := *g
	out.Rules = rules
	return &out
}

func servedPolicy(p *PolicyConfig) *PolicyConfig {
	if p == nil {
		return nil
	}
	rules, changed := servedRules(p.Rules)
	if !changed {
		return p
	}
	out := *p
	out.Rules = rules
	return &out
}

// walkConfigType visits every JSON field reachable from t, depth first, each
// named by its dotted path. It is the walk the schema and the header share:
// jsonFields names a struct's fields and structOf finds the struct a field
// holds. A type already on the path is not entered again.
func walkConfigType(t reflect.Type, prefix string, visit func(path string, f jsonField)) {
	var walk func(t reflect.Type, prefix string, stack []reflect.Type)
	walk = func(t reflect.Type, prefix string, stack []reflect.Type) {
		if slices.Contains(stack, t) {
			return
		}
		stack = append(stack, t)
		for _, f := range jsonFields(t) {
			path := joinKey(prefix, f.name)
			visit(path, f)
			if st := structOf(f.typ); st != nil {
				walk(st, path, stack)
			}
		}
	}
	walk(t, prefix, nil)
}

// walkConfigValue is walkConfigType over a document: it visits every JSON
// field with its value. where names the enclosing listener, for the refusal
// an admin reads, and path is the field's key under it. Config structs hold
// no embedded structs, and this walk does not flatten one.
func walkConfigValue(v reflect.Value, where, prefix string,
	visit func(where, path string, f jsonField, v reflect.Value)) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	switch v.Kind() {
	case reflect.Struct:
		t := v.Type()
		for i := range t.NumField() {
			sf := t.Field(i)
			name, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
			if name == "-" || !sf.IsExported() {
				continue
			}
			if name == "" {
				name = sf.Name
			}
			path := joinKey(prefix, name)
			fv := v.Field(i)
			visit(where, path, jsonField{name: name, typ: sf.Type, tag: sf.Tag}, fv)
			walkConfigValue(fv, where, path, visit)
		}
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return
		}
		for i := range v.Len() {
			el := v.Index(i)
			w, p := where, fmt.Sprintf("%s[%d]", prefix, i)
			if l, ok := el.Interface().(ListenerConfig); ok {
				w, p = fmt.Sprintf("listener %q", l.Name), ""
			}
			walkConfigValue(el, w, p, visit)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			walkConfigValue(iter.Value(), where, fmt.Sprintf("%s.%v", prefix, iter.Key()), visit)
		}
	}
}
