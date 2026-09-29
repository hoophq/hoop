package daemon

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
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
//     (every field omits it), so an older build never sees the key.
//   - rule:<type> is a guardrail rule type policy.RuleTypes lists.
//   - protocol:<name> is a listener protocol Protocols lists.
const (
	// CapabilityReviewMode means this build decodes an analyzer block's
	// review_mode.
	CapabilityReviewMode = "review_mode"

	capabilityRulePrefix     = "rule:"
	capabilityProtocolPrefix = "protocol:"
)

// capabilitySince names the first release that sends an entry, for the
// refusal an admin reads. A hoop release, because `hoop start sidecar` is the
// shipped binary. An entry without one is refused with a generic hint.
var capabilitySince = map[string]string{
	CapabilityReviewMode: "1.196.0",
}

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

// SidecarCapabilities is what this build sends in CapabilitiesHeader: every
// cap-tagged field of the config, every rule type and every protocol it
// links. Sorted, so two builds that decode the same document send the same
// header.
func SidecarCapabilities() []string {
	var out []string
	walkConfigType(reflect.TypeFor[Config](), "", func(_ string, f jsonField) {
		if c := f.tag.Get("cap"); c != "" {
			out = append(out, c)
		}
	})
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

// CheckServable refuses a document that a sidecar reporting caps cannot
// decode: a set field whose entry it lacks, a rule of a type it does not
// list, or a listener protocol it does not speak. It reads the served form,
// so a default the plane never serves needs no entry. The error names the
// listener and the release that adds support.
func CheckServable(cfg Config, caps []string) error {
	has := map[string]bool{}
	for _, c := range effectiveCapabilities(caps) {
		has[c] = true
	}
	var problems []string
	need := func(where, what, capability string) {
		if has[capability] {
			return
		}
		problems = append(problems, fmt.Sprintf("%s %s, and this sidecar does not support it; %s",
			where, what, upgradeHint(capability)))
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

func upgradeHint(capability string) string {
	if since, ok := capabilitySince[capability]; ok {
		return fmt.Sprintf("upgrade the sidecar to %s or later", since)
	}
	return fmt.Sprintf("upgrade the sidecar to a release that reports %q", capability)
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
