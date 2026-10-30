package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/policy"
)

// This file is ENG-662: per risk level, the AI may apply existing Guardrails
// and Data Masking rules to a statement, and may create new ones. The level
// options live in analyzer.ai_rules; the existing rules live in the
// top-level rule_library.

// CapabilityAIRules means this build reads analyzer.ai_rules and the
// top-level rule_library, and applies the rules the AI selects. The cap tags
// on those fields put it in the header, so CheckServable refuses a document
// that sets either to a build without it. No downgrade exists: an older
// build has no way to apply an AI-selected rule.
const CapabilityAIRules = "analyzer_ai_rules"

// LevelAIRules holds the AI rule options of each risk level of an analyzer
// block.
type LevelAIRules struct {
	High   *LevelAIRule `json:"high,omitempty"`
	Medium *LevelAIRule `json:"medium,omitempty"`
	Low    *LevelAIRule `json:"low,omitempty"`
}

// LevelAIRule is the AI rule options of one level. They are not the
// listener's guardrails and mask blocks, which apply as before. An entry
// sets one of the two at least.
type LevelAIRule struct {
	// Guardrails lets the AI apply rule_library guardrails.
	Guardrails *AIRuleOptions `json:"guardrails,omitempty"`

	// Mask lets the AI mask the response with rule_library mask rules.
	Mask *AIRuleOptions `json:"mask,omitempty"`
}

// AIRuleOptions are the AI rule options of one feature at one risk level.
type AIRuleOptions struct {
	// Mode is existing_only (the default) or existing_and_new. Any other
	// value refuses the config.
	Mode string `json:"mode,omitempty"`

	// Rules names the rule_library rules of this feature the AI may
	// apply. Absent allows every one; empty allows none. A name
	// rule_library does not hold is warned about and ignored.
	Rules []string `json:"rules,omitzero"`
}

// LevelAIRule returns the AI rule options the block sets for level, or nil.
func (la *LaneAnalyzerConfig) LevelAIRule(level analyzer.RiskLevel) *LevelAIRule {
	if la == nil || la.AIRules == nil {
		return nil
	}
	switch level {
	case analyzer.RiskHigh:
		return la.AIRules.High
	case analyzer.RiskMedium:
		return la.AIRules.Medium
	case analyzer.RiskLow:
		return la.AIRules.Low
	}
	return nil
}

// isZero reports a block with no level set.
func (a *LevelAIRules) isZero() bool {
	return a == nil || a.High == nil && a.Medium == nil && a.Low == nil
}

// RuleLibrary holds the existing rules a risk level may let the AI apply.
// Every rule needs a unique name: a level names the rules it allows.
type RuleLibrary struct {
	// Guardrails are guardrail rules, as guardrails.rules writes them.
	// A rule that does not deny on a match (action: defer, ai_analysis)
	// is warned about and never offered.
	Guardrails []policy.Rule `json:"guardrails,omitempty"`

	// Mask are data masking rules, as mask.rules writes them: the plugin
	// owns their shape.
	Mask json.RawMessage `json:"mask,omitempty"`
}

// maskEntry is one rule_library mask rule.
type maskEntry struct {
	name string
	def  json.RawMessage
}

// maskEntries decodes the mask rules. Shape errors are validate's to report.
func (l *RuleLibrary) maskEntries() ([]maskEntry, error) {
	if l == nil || len(bytes.TrimSpace(l.Mask)) == 0 {
		return nil, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(l.Mask, &raw); err != nil {
		return nil, fmt.Errorf("rule_library.mask is not a JSON array of rules: %w", err)
	}
	out := make([]maskEntry, 0, len(raw))
	for i, r := range raw {
		var head struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(r, &head); err != nil {
			return nil, fmt.Errorf("rule_library.mask[%d] is not a rule object: %w", i, err)
		}
		out = append(out, maskEntry{name: head.Name, def: r})
	}
	return out, nil
}

// validate refuses a library a level could not name rules from: a rule with
// no name, or two with one name. Each rule's own validity is checked where
// the lane is built, and an invalid one is warned about and ignored.
func (l *RuleLibrary) validate() []string {
	if l == nil {
		return nil
	}
	var problems []string
	seen := map[string]bool{}
	for i, r := range l.Guardrails {
		switch {
		case strings.TrimSpace(r.Name) == "":
			problems = append(problems, fmt.Sprintf("rule_library.guardrails[%d] has no name; a risk level "+
				"names the rules it allows", i))
		case seen[r.Name]:
			problems = append(problems, fmt.Sprintf("rule_library.guardrails names %q twice", r.Name))
		}
		seen[r.Name] = true
	}
	masks, err := l.maskEntries()
	if err != nil {
		return append(problems, err.Error())
	}
	seen = map[string]bool{}
	for i, m := range masks {
		switch {
		case strings.TrimSpace(m.name) == "":
			problems = append(problems, fmt.Sprintf("rule_library.mask[%d] has no name; a risk level "+
				"names the rules it allows", i))
		case seen[m.name]:
			problems = append(problems, fmt.Sprintf("rule_library.mask names %q twice", m.name))
		}
		seen[m.name] = true
	}
	return problems
}

// appliesAIRules reports an action that applies AI rules: allow, warn,
// require_approval and ai-handoff. An unset action is allow.
func appliesAIRules(action string) bool {
	switch analyzer.Action(action).Canonical() {
	case "", analyzer.ActionAllow, analyzer.ActionWarn, analyzer.ActionRequireReview, analyzer.ActionHandoff:
		return true
	}
	return false
}

// validateLevelAIRules refuses AI rule options the sidecar cannot act on: an
// entry on a level whose action applies no AI rule, an entry that sets
// neither guardrails nor mask, and an unknown mode.
func validateLevelAIRules(la *LaneAnalyzerConfig, where string) []string {
	var problems []string
	for _, l := range la.levels() {
		r := la.LevelAIRule(l.risk)
		if r == nil {
			continue
		}
		at := fmt.Sprintf("%s: ai_rules.%s", where, l.name)
		if !appliesAIRules(*l.action) {
			problems = append(problems, fmt.Sprintf("%s lets the AI apply rules, and the %s risk action %q "+
				"applies none (only allow, warn, %s and %s do); change the action or remove the entry",
				at, l.name, *l.action, analyzer.ActionRequireApproval, analyzer.ActionHandoff))
		}
		if r.Guardrails == nil && r.Mask == nil {
			problems = append(problems, fmt.Sprintf("%s sets neither guardrails nor mask; set one or "+
				"remove the entry", at))
		}
		for _, f := range [...]struct {
			key string
			o   *AIRuleOptions
		}{{"guardrails", r.Guardrails}, {"mask", r.Mask}} {
			if f.o != nil && !analyzer.RuleMode(f.o.Mode).Valid() {
				problems = append(problems, fmt.Sprintf("%s: %s.mode %q is unknown; use %s or %s",
					at, f.key, f.o.Mode, analyzer.RuleModeExistingOnly, analyzer.RuleModeExistingAndNew))
			}
		}
	}
	return problems
}

// aiRuleLevel is one level whose AI rule options apply.
type aiRuleLevel struct {
	laneLevel
	rule *LevelAIRule
}

// aiRuleLevels lists the levels whose AI rule options apply.
func (la *LaneAnalyzerConfig) aiRuleLevels() []aiRuleLevel {
	if la == nil || la.AIRules == nil {
		return nil
	}
	var out []aiRuleLevel
	for _, l := range la.levels() {
		if r := la.LevelAIRule(l.risk); r != nil && appliesAIRules(*l.action) {
			out = append(out, aiRuleLevel{l, r})
		}
	}
	return out
}

// usesAIMask reports a lane where the AI may mask a response.
func (la *LaneAnalyzerConfig) usesAIMask() bool {
	return slices.ContainsFunc(la.aiRuleLevels(), func(l aiRuleLevel) bool { return l.rule.Mask != nil })
}

// aiMaskScope is how a lane ties an AI mask to the response of its
// statement; see gate.MaskScope. Protocols that cannot are MaskScopeNone, and
// the gate denies a statement the AI masks there.
//
//   - grpc and spanner: one gate per call, so a mask covers the call's
//     responses. It needs a descriptor set, as a mask block does. An AI
//     mask names entities, not fields, so the lane must decode every
//     response field: not grpc.raw, which rewrites only the declared
//     string fields and forwards the rest, and not grpc.discover.fail_open,
//     which forwards what it cannot decode while discovery fails.
//   - http: HTTP/1 answers in request order.
//   - postgres, mysql, oracle, clickhouse: one statement in flight.
//   - mssql: the pinned rewriter forwards SQL_VARIANT, XML and over-limit
//     result data unmasked, and stops rewriting for the connection,
//     without reporting either; until it reports them, no AI mask.
//   - mongodb: replies pair with requests by id, and an unacknowledged
//     write or an exhaust cursor has no one-to-one answer.
//   - ssh: a session's output is not tied to one command.
//   - ica and plug-in protocols: no statement response the sidecar can
//     tie, or framing this build does not know.
func aiMaskScope(lc ListenerConfig) gate.MaskScope {
	switch {
	case isGRPCTransport(lc):
		if grpcAIMaskGap(lc.GRPC) == "" {
			return gate.MaskScopeExchange
		}
		return gate.MaskScopeNone
	case isSSH(lc), isICA(lc), isPluginLane(lc):
		return gate.MaskScopeNone
	}
	switch p := inspect.Protocol(lc.Protocol); p {
	case inspect.HTTP:
		return gate.MaskScopeHTTP
	case inspect.Postgres, inspect.MySQL, inspect.Oracle, inspect.ClickHouse:
		if gate.MaskSupported(p) {
			return gate.MaskScopeSerial
		}
	}
	return gate.MaskScopeNone
}

// grpcAIMaskGap says why a grpc lane cannot apply an AI mask to every field
// of a response, or "".
func grpcAIMaskGap(g *GRPCCodecConfig) string {
	switch {
	case !g.hasSchema():
		return "cannot tie a response to one statement"
	case g.Raw != nil:
		return "masks only the response fields grpc.raw.string_fields declares"
	case g.Discover != nil && g.Discover.FailOpen:
		return "forwards what it cannot decode while discovery fails (grpc.discover.fail_open)"
	}
	return ""
}

// noAIMaskReason says why a lane is MaskScopeNone, for LaneAIRuleWarnings.
func noAIMaskReason(lc ListenerConfig) string {
	if isGRPCTransport(lc) {
		return grpcAIMaskGap(lc.GRPC)
	}
	return "cannot tie a response to one statement"
}

// laneMaskScope is the scope a lane's gates run under: aiMaskScope when the
// lane's analyzer may mask a response, none otherwise, so a lane without AI
// masks rewrites nothing it did not before.
func laneMaskScope(lc ListenerConfig) gate.MaskScope {
	if !lc.Analyzer.usesAIMask() {
		return gate.MaskScopeNone
	}
	return aiMaskScope(lc)
}

// masksAtBuild reports an endpoint lane whose server is built to rewrite
// responses: a mask block, or an AI mask on a grpc call.
func (ln lane) masksAtBuild() bool {
	return ln.masker != nil || ln.maskScope == gate.MaskScopeExchange
}

// LaneAIRuleWarnings lists what a lane's AI rule options do not do: a name
// rule_library does not hold, a level left with nothing to apply, and AI
// masks on a protocol that cannot tie a response to one statement. None is
// refused. Exported so the control plane can show them at the save.
func LaneAIRuleWarnings(lib *RuleLibrary, la *LaneAnalyzerConfig, lc ListenerConfig) []string {
	var out []string
	guards := map[string]bool{}
	if lib != nil {
		for _, r := range lib.Guardrails {
			guards[r.Name] = true
		}
	}
	masks := map[string]bool{}
	entries, _ := lib.maskEntries()
	for _, m := range entries {
		masks[m.name] = true
	}
	scope := aiMaskScope(lc)
	for _, l := range la.aiRuleLevels() {
		r := l.rule
		for _, f := range [...]struct {
			key   string
			o     *AIRuleOptions
			known map[string]bool
		}{{"guardrails", r.Guardrails, guards}, {"mask", r.Mask, masks}} {
			if f.o == nil {
				continue
			}
			for _, name := range f.o.Rules {
				if !f.known[name] {
					out = append(out, fmt.Sprintf("analyzer block: ai_rules.%s.%s.rules names %q, which "+
						"rule_library does not hold, so it is ignored", l.name, f.key, name))
				}
			}
		}
		if r.Mask != nil && scope == gate.MaskScopeNone {
			out = append(out, fmt.Sprintf("analyzer block: ai_rules.%s.mask: a %s lane %s, so a statement "+
				"the AI masks is denied", l.name, lc.Protocol, noAIMaskReason(lc)))
		}
		if r.Mask != nil && scope == gate.MaskScopeSerial && !gate.ResponseEndKnown(inspect.Protocol(lc.Protocol)) {
			out = append(out, fmt.Sprintf("analyzer block: ai_rules.%s.mask: a %s lane cannot see where a "+
				"response ends, so after a statement the AI masks the connection takes no further "+
				"statement", l.name, lc.Protocol))
		}
	}
	return out
}

// aiRuleUse reports which AI rule features a config uses: a level option or
// rule_library rules of that kind.
func (c *Config) aiRuleUse() (guardrails, mask bool) {
	if c.RuleLibrary != nil {
		guardrails = len(c.RuleLibrary.Guardrails) > 0
		n, _ := countMaskRules(c.RuleLibrary.Mask)
		mask = n > 0
	}
	for _, lc := range c.Listeners {
		if lc.Analyzer == nil {
			continue
		}
		for _, l := range lc.Analyzer.levels() {
			if r := lc.Analyzer.LevelAIRule(l.risk); r != nil {
				guardrails = guardrails || r.Guardrails != nil
				mask = mask || r.Mask != nil
			}
		}
	}
	return guardrails, mask
}

// aiRuleLicenseProblems refuses AI rule options and rule_library without an
// Enterprise license that grants the feature: guardrails for the guardrail
// options, data masking for the mask options. The library rules count
// against no free-tier cap: the feature is licensed anyway.
func (c *Config) aiRuleLicenseProblems(lic license.Status) []string {
	guardrails, mask := c.aiRuleUse()
	var problems []string
	if guardrails && !lic.Allows(license.FeatureGuardrails) {
		problems = append(problems, "AI-applied guardrails (analyzer.ai_rules guardrails, or "+
			"rule_library.guardrails) need an Enterprise license that grants guardrails."+licenseAdvice(lic))
	}
	if mask && !lic.Allows(license.FeatureDataMasking) {
		problems = append(problems, "AI-applied data masking (analyzer.ai_rules mask, or rule_library.mask) "+
			"needs an Enterprise license that grants data masking."+licenseAdvice(lic))
	}
	return problems
}

// laneAIRules is what buildAnalyzerEvaluator hands the analyzer for one
// lane's AI rule options: the resolved levels and the compilers.
type laneAIRules struct {
	levels           map[analyzer.RiskLevel]analyzer.LevelRules
	compileGuardrail func(policy.Rule) (policy.Evaluator, error)
	compileMask      func(json.RawMessage) (policy.ResponseMask, error)
	onInvalid        func(analyzer.RiskLevel, string, error)
}

// buildLaneAIRules resolves a lane's AI rule options against rule_library.
// A library rule that fails the validation a config rule gets on this lane,
// or that the AI cannot apply (it does not deny, or an ssh lane cannot carry
// its strategy), is warned about and left out. A rule the AI creates gets
// the same validation, and create_guardrail offers no type the lane cannot
// evaluate. Mask options with no detector refuse the lane, as a mask block
// does.
func buildLaneAIRules(lib *RuleLibrary, la *LaneAnalyzerConfig, lc ListenerConfig, det Plugin,
	log *slog.Logger) (*laneAIRules, []string, error) {
	levels := la.aiRuleLevels()
	if len(levels) == 0 {
		return nil, nil, nil
	}
	notes := LaneAIRuleWarnings(lib, la, lc)
	out := &laneAIRules{levels: map[analyzer.RiskLevel]analyzer.LevelRules{}}

	ssh := isSSH(lc)
	out.compileGuardrail = func(r policy.Rule) (policy.Evaluator, error) {
		if r.Action != "" {
			return nil, fmt.Errorf("action %q: an AI guardrail denies on a match and takes no action", r.Action)
		}
		if p := laneGuardrailProblems(r, lc, det); len(p) > 0 {
			return nil, errors.New(strings.Join(p, "; "))
		}
		if det != nil {
			return policy.NewRulesWithScanner([]policy.Rule{r}, det)
		}
		return policy.NewRules([]policy.Rule{r})
	}
	if la.usesAIMask() {
		if det == nil {
			return nil, nil, errors.New("analyzer block: ai_rules lets the AI mask a response, and this " +
				"build has no detection plugin to mask with")
		}
		out.compileMask = func(def json.RawMessage) (policy.ResponseMask, error) {
			list := append(append([]byte{'['}, def...), ']')
			if ssh {
				if p := validateSSHMasking(MaskConfig{Rules: list}, lc.Name); len(p) > 0 {
					return nil, errors.New(strings.Join(p, "; "))
				}
			}
			return det.BuildMasker(list)
		}
	}
	if log != nil {
		lane := lc.Name
		out.onInvalid = func(level analyzer.RiskLevel, kind string, err error) {
			log.Warn("the AI created an invalid rule; it was dropped and recorded",
				"listener", lane, "risk", level, "kind", kind, "error", err)
		}
	}

	// Each library rule is compiled once here, so a bad one is said once
	// per lane and never reaches the analyzer.
	var guards []policy.Rule
	if lib != nil {
		for _, r := range lib.Guardrails {
			if r.Action != "" || r.Type == policy.MatchAIAnalysis {
				notes = append(notes, fmt.Sprintf("rule_library guardrail %q does not deny on a match, so "+
					"the AI cannot apply it; it is ignored", r.Name))
				continue
			}
			if _, err := out.compileGuardrail(r); err != nil {
				notes = append(notes, fmt.Sprintf("rule_library guardrail %q is invalid and ignored: %v", r.Name, err))
				continue
			}
			guards = append(guards, r)
		}
	}
	var masks []analyzer.MaskRule
	if out.compileMask != nil {
		entries, _ := lib.maskEntries()
		for _, m := range entries {
			if _, err := out.compileMask(m.def); err != nil {
				notes = append(notes, fmt.Sprintf("rule_library mask rule %q is invalid on this lane and "+
					"ignored: %v", m.name, err))
				continue
			}
			masks = append(masks, analyzer.MaskRule{Name: m.name, Definition: m.def})
		}
	}

	types := slices.DeleteFunc(slices.Clone(analyzer.DefaultGuardrailTypes), func(t policy.MatchType) bool {
		return t == policy.MatchPII && (det == nil || len(det.Entities()) == 0) ||
			ssh && len(policy.ValidateForSSH([]policy.Rule{{Type: t}})) > 0
	})
	var strategies []string
	if ssh {
		strategies = []string{sshOnlyMaskStrategy}
	}
	for _, l := range levels {
		r := l.rule
		var lr analyzer.LevelRules
		if r.Guardrails != nil {
			lr.Guardrails = &analyzer.GuardrailOptions{
				Mode:  analyzer.RuleMode(r.Guardrails.Mode),
				Rules: selectRules(guards, r.Guardrails.Rules, func(g policy.Rule) string { return g.Name }),
				Types: types,
			}
		}
		if r.Mask != nil {
			lr.Mask = &analyzer.MaskOptions{
				Mode:       analyzer.RuleMode(r.Mask.Mode),
				Rules:      selectRules(masks, r.Mask.Rules, func(m analyzer.MaskRule) string { return m.Name }),
				Strategies: strategies,
			}
		}
		empty := func(mode analyzer.RuleMode, n int) bool { return mode != analyzer.RuleModeExistingAndNew && n == 0 }
		if (lr.Guardrails == nil || empty(lr.Guardrails.Mode, len(lr.Guardrails.Rules))) &&
			(lr.Mask == nil || empty(lr.Mask.Mode, len(lr.Mask.Rules))) {
			notes = append(notes, fmt.Sprintf("analyzer block: ai_rules.%s lets the AI apply no rule and "+
				"create none, so it has nothing to apply", l.name))
		}
		out.levels[l.risk] = lr
	}
	return out, notes, nil
}

// laneGuardrailProblems runs the lane checks a guardrails block gets on one
// AI guardrail: pii entities the detector finds, the rule types an ssh
// statement can answer, and the headers an http lane captures.
func laneGuardrailProblems(r policy.Rule, lc ListenerConfig, det Plugin) []string {
	rules := []policy.Rule{r}
	problems := checkPIIEntities(rules, det)
	if isSSH(lc) {
		problems = append(problems, validateSSHRules(rules, lc.Name)...)
	}
	if inspect.Protocol(lc.Protocol) == inspect.HTTP {
		problems = append(problems, validateHTTPHeaderRules(rules, lc.HTTP, lc.Name)...)
	}
	return problems
}

// selectRules keeps the rules names allows, in library order. Nil names
// allow every rule; a name the library does not hold is ignored
// (LaneAIRuleWarnings says so).
func selectRules[T any](all []T, names []string, name func(T) string) []T {
	if names == nil {
		return slices.Clone(all)
	}
	var out []T
	for _, r := range all {
		if slices.Contains(names, name(r)) {
			out = append(out, r)
		}
	}
	return out
}
