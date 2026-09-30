package daemon

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/policy"
)

// CapabilitiesHeader lists, comma separated, what this build decodes of the
// served document. The sidecar sends it on every handshake.
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

// Three kinds of entry travel in the header. SidecarCapabilities generates
// all three from what the build links, so a new field, rule type or protocol
// is reported without anyone listing it (ADR-0022):
//
//   - A bare name is a FIELD: the cap:"<name>" tag on the struct field that
//     decodes it. A document that sets the field needs the entry. A field
//     that is safe when absent needs no tag: the plane serves no zero value
//     (every field omits it), so an older build never sees the key. A
//     since:"<release>" tag beside it names the release the field shipped
//     in; see CheckServable for what it decides.
//   - rule:<type> is a guardrail rule type policy.RuleTypes lists.
//   - protocol:<name> is a listener protocol Protocols lists.
const (
	// CapabilityReviewMode means this build decodes an analyzer block's
	// review_mode.
	CapabilityReviewMode = "review_mode"
	// CapabilityAnalyzerRateLimit means this build decodes analyzer
	// rate_limit, on the top-level section and on a listener's block.
	CapabilityAnalyzerRateLimit = "analyzer_rate_limit"

	capabilityRulePrefix     = "rule:"
	capabilityProtocolPrefix = "protocol:"
)

// baselineCapabilities is the rule and protocol vocabulary of the last
// release whose header carried no rule: or protocol: entry. A handshake that
// names none of a kind comes from such a build, and it decodes exactly this;
// effectiveCapabilities fills it in.
//
// FROZEN. An entry added here is granted to every build that predates the
// generated list, and such a build would refuse it. A new rule type or
// protocol reaches the header through policy.RuleTypes and Protocols alone.
var baselineCapabilities = []string{
	"protocol:clickhouse", "protocol:grpc", "protocol:http", "protocol:mongodb",
	"protocol:mssql", "protocol:mysql", "protocol:postgres", "protocol:spanner",
	"protocol:ssh",
	"rule:ai_analysis", "rule:deny_words_list", "rule:grpc_status",
	"rule:http_header", "rule:http_resource", "rule:http_status",
	"rule:operation", "rule:pattern_match", "rule:pii", "rule:table",
}

// Handshake is what a sidecar said about itself, as far as serving needs it.
type Handshake struct {
	// Version is the release the sidecar reported. It is read only for a
	// build whose header predates the generated list; see CheckServable.
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

// capabilitySince maps a field entry to the release it shipped in, for every
// field whose tag says. Read from the struct, so the number sits beside the
// field it describes.
func capabilitySince() map[string]string {
	out := map[string]string{}
	for _, f := range capFields() {
		if f.since != "" {
			out[f.name] = f.since
		}
	}
	return out
}

// SidecarCapabilities is what this build sends in CapabilitiesHeader: every
// cap-tagged field of the config, every rule type and every protocol it
// links. Sorted, so two builds that decode the same document send the same
// header.
func SidecarCapabilities() []string {
	var out []string
	for _, f := range capFields() {
		out = append(out, f.name)
	}
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

// effectiveCapabilities widens a reported list to what its build decodes. A
// list with no entry of a kind predates that kind's generated list, so it
// gets the frozen baseline. It never narrows: an entry a build reports is
// one it decodes.
func effectiveCapabilities(caps []string) []string {
	out := slices.Clone(caps)
	for _, prefix := range []string{capabilityRulePrefix, capabilityProtocolPrefix} {
		if slices.ContainsFunc(caps, func(c string) bool { return strings.HasPrefix(c, prefix) }) {
			continue
		}
		for _, b := range baselineCapabilities {
			if strings.HasPrefix(b, prefix) {
				out = append(out, b)
			}
		}
	}
	return out
}

// predatesGeneratedList reports whether a header comes from a build older
// than the generated list: such a header names no rule: entry, because every
// build that generates the list links at least one rule type.
func predatesGeneratedList(caps []string) bool {
	return !slices.ContainsFunc(caps, func(c string) bool { return strings.HasPrefix(c, capabilityRulePrefix) })
}

// CheckServable refuses a document that the sidecar behind hs cannot decode:
// a set field whose entry it lacks, a rule of a type it does not list, or a
// listener protocol it does not speak. It reads the served form, so a default
// the plane never serves needs no entry. The error names the listener and the
// release that adds support.
//
// A build from before the generated list names no field but the few it was
// taught by hand, so for such a build a field's since release stands in for
// the missing entry: the field is granted when the reported release parses
// and reaches it. A release that does not parse (a dev build's "unknown")
// grants nothing. A build that sends the generated list is read from the
// list alone; its release decides nothing.
func CheckServable(cfg Config, hs Handshake) error {
	has := map[string]bool{}
	for _, c := range effectiveCapabilities(hs.Capabilities) {
		has[c] = true
	}
	since := capabilitySince()
	reported, reportedKnown := parseRelease(hs.Version)
	byRelease := predatesGeneratedList(hs.Capabilities) && reportedKnown
	granted := func(capability string) bool {
		if has[capability] {
			return true
		}
		shipped, ok := parseRelease(since[capability])
		return byRelease && ok && !releaseBefore(reported, shipped)
	}
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
	served := ServedForm(cfg)
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
// The one hand-written case: "hold" is a value, and every other default is
// the zero value, which the field's own tag omits.
func ServedForm(cfg Config) Config {
	listeners := make([]ListenerConfig, len(cfg.Listeners))
	copy(listeners, cfg.Listeners)
	for i, l := range listeners {
		if l.Analyzer == nil || l.Analyzer.ReviewMode != "hold" {
			continue
		}
		block := *l.Analyzer
		block.ReviewMode = ""
		listeners[i].Analyzer = &block
	}
	cfg.Listeners = listeners
	return cfg
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
