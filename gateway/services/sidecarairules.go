package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	commonlicense "github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/policy"
	"gorm.io/gorm"
)

// ENG-662: an analyzer risk level may let the AI apply rules of the
// organization's guardrail and data masking libraries to one statement. The
// level names them in its guardrails and mask options; composition serves
// them in the document's rule_library.

// aiRuleKind is one feature whose library rules a level may let the AI apply.
type aiRuleKind struct {
	// key is the level field, as the sidecar spells it.
	key string
	// label names the library in a refusal.
	label string
	// feature is the license feature the kind needs.
	feature string
	options func(*daemon.RiskLevel) **daemon.AIRuleOptions
	library func(*gorm.DB, uuid.UUID) ([]models.LibraryRule, error)
}

var aiRuleKinds = [...]aiRuleKind{
	{
		key: "guardrails", label: "guardrail", feature: commonlicense.FeatureGuardrails,
		options: func(r *daemon.RiskLevel) **daemon.AIRuleOptions { return &r.Guardrails },
		library: models.ListGuardrailSidecarLibrary,
	},
	{
		key: "mask", label: "data masking", feature: commonlicense.FeatureDataMasking,
		options: func(r *daemon.RiskLevel) **daemon.AIRuleOptions { return &r.Mask },
		library: models.ListDataMaskingSidecarLibrary,
	},
}

const (
	aiKindGuardrails = 0
	aiKindMask       = 1
)

// namedLevel is one risk level of an analyzer block.
type namedLevel struct {
	name  string
	level *daemon.RiskLevel
}

func analyzerLevels(la *daemon.LaneAnalyzerConfig) []namedLevel {
	return []namedLevel{{"high", &la.HighRisk}, {"medium", &la.MediumRisk}, {"low", &la.LowRisk}}
}

// appliesAIRules reports a level whose action applies AI rules: allow, warn,
// require_approval and ai-handoff. An unset action is allow. It is the
// sidecar's RiskLevel.appliesAIRules, which the daemon does not export; on
// block and defer the sidecar ignores the options.
func appliesAIRules(r daemon.RiskLevel) bool {
	switch analyzer.Action(r.Action).Canonical() {
	case "", analyzer.ActionAllow, analyzer.ActionWarn, analyzer.ActionRequireReview, analyzer.ActionHandoff:
		return true
	}
	return false
}

// aiRuleSelection is what the levels of a document let the AI apply of one
// kind: every library rule, or the named ones.
type aiRuleSelection struct {
	used  bool
	all   bool
	names []string
}

func (s aiRuleSelection) selects(row string) bool {
	return s.all || slices.Contains(s.names, row)
}

// selectAIRules reads the selections of every lane's analyzer block. A level
// whose action applies no rule selects nothing: the sidecar ignores it.
func selectAIRules(cfg daemon.Config) [len(aiRuleKinds)]aiRuleSelection {
	var out [len(aiRuleKinds)]aiRuleSelection
	for _, l := range cfg.Listeners {
		if l.Analyzer == nil {
			continue
		}
		for _, lv := range analyzerLevels(l.Analyzer) {
			if !appliesAIRules(*lv.level) {
				continue
			}
			for k, kind := range aiRuleKinds {
				o := *kind.options(lv.level)
				if o == nil {
					continue
				}
				out[k].used = true
				if o.Rules == nil {
					out[k].all = true
				}
				for _, n := range o.Rules {
					if !slices.Contains(out[k].names, n) {
						out[k].names = append(out[k].names, n)
					}
				}
			}
		}
	}
	return out
}

// libraryEntryNames are the rule_library names of one library row's rules.
// A row with one rule serves it under the row's name, which is the name a
// level writes. A row with several serves each as row/rule, or as row/N when
// the rule names are not unique, and the levels name all of them.
func libraryEntryNames(row string, ruleNames []string) []string {
	if len(ruleNames) == 1 {
		return []string{row}
	}
	unique := true
	seen := map[string]bool{}
	for _, n := range ruleNames {
		if strings.TrimSpace(n) == "" || seen[n] {
			unique = false
			break
		}
		seen[n] = true
	}
	out := make([]string, len(ruleNames))
	for i, n := range ruleNames {
		if !unique {
			n = strconv.Itoa(i + 1)
		}
		out[i] = row + "/" + n
	}
	return out
}

// libraryGuardrails decodes a guardrail library row: its rules, or nil when
// the row has none a sidecar decodes. Such a row is left out of rule_library;
// a level naming it gets a warning from the sidecar, as for any unknown name.
func libraryGuardrails(row models.LibraryRule) []policy.Rule {
	var block daemon.GuardrailsConfig
	if decodeSpec(row.Spec, &block) != nil {
		return nil
	}
	return block.Rules
}

// libraryMasks decodes a data masking library row into its rules, each an
// object whose shape the detector plugin owns, and their names.
func libraryMasks(row models.LibraryRule) ([]map[string]json.RawMessage, []string) {
	var block struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if decodeSpec(row.Spec, &block) != nil {
		return nil, nil
	}
	rules := make([]map[string]json.RawMessage, 0, len(block.Rules))
	names := make([]string, 0, len(block.Rules))
	for _, raw := range block.Rules {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return nil, nil
		}
		var name string
		if n, ok := fields["name"]; ok && json.Unmarshal(n, &name) != nil {
			return nil, nil
		}
		rules = append(rules, fields)
		names = append(names, name)
	}
	return rules, names
}

// composeRuleLibrary serves in rule_library the library rules the document's
// analyzer levels let the AI apply (foldRuleLibrary). It reads only the
// libraries a level uses.
//
// rule_library is not part of the sidecar's baseline document, so a library
// edit reaches a running sidecar at its next handshake, as a rule edit does.
func composeRuleLibrary(db *gorm.DB, orgID uuid.UUID, cfg daemon.Config) (daemon.Config, error) {
	sel := selectAIRules(cfg)
	var rows [len(aiRuleKinds)][]models.LibraryRule
	for k, kind := range aiRuleKinds {
		if !sel[k].used {
			continue
		}
		var err error
		if rows[k], err = kind.library(db, orgID); err != nil {
			return cfg, fmt.Errorf("%w: failed loading the %s rule library: %v", ErrSidecarRulesUnavailable, kind.label, err)
		}
	}
	return foldRuleLibrary(cfg, rows)
}

// foldRuleLibrary puts in rule_library the rows the levels select: every
// row of a kind a level selects with no rules list, else the named ones, and
// nothing of a kind no level uses. A row with no sidecar rule is left out.
// The levels' lists are rewritten to the served names (libraryEntryNames).
// A rule_library the stored configuration writes itself is kept, and a name
// in both is refused: the sidecar refuses a library with a name twice.
func foldRuleLibrary(cfg daemon.Config, rows [len(aiRuleKinds)][]models.LibraryRule) (daemon.Config, error) {
	sel := selectAIRules(cfg)
	if !sel[aiKindGuardrails].used && !sel[aiKindMask].used {
		return cfg, nil
	}

	var lib daemon.RuleLibrary
	if cfg.RuleLibrary != nil {
		lib = *cfg.RuleLibrary
	}
	held := [len(aiRuleKinds)]map[string]bool{{}, {}}
	for _, r := range lib.Guardrails {
		held[aiKindGuardrails][r.Name] = true
	}
	var masks []json.RawMessage
	var storedMasksErr error
	if len(lib.Mask) > 0 {
		storedMasksErr = json.Unmarshal(lib.Mask, &masks)
		for _, m := range masks {
			var head struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(m, &head) == nil {
				held[aiKindMask][head.Name] = true
			}
		}
	}
	claim := func(k int, name string) error {
		if held[k][name] {
			return fmt.Errorf("the %s rule library serves %q twice: the sidecar's configuration holds a "+
				"rule_library rule by that name; rename one of them", aiRuleKinds[k].label, name)
		}
		held[k][name] = true
		return nil
	}
	expand := [len(aiRuleKinds)]map[string][]string{{}, {}}

	guardrails := slices.Clone(lib.Guardrails)
	for _, row := range rows[aiKindGuardrails] {
		if !sel[aiKindGuardrails].selects(row.Name) {
			continue
		}
		rules := libraryGuardrails(row)
		if len(rules) == 0 {
			continue
		}
		ruleNames := make([]string, len(rules))
		for i, r := range rules {
			ruleNames[i] = r.Name
		}
		names := libraryEntryNames(row.Name, ruleNames)
		for i, r := range rules {
			if err := claim(aiKindGuardrails, names[i]); err != nil {
				return cfg, err
			}
			r.Name = names[i]
			guardrails = append(guardrails, r)
		}
		expand[aiKindGuardrails][row.Name] = names
	}

	added := false
	for _, row := range rows[aiKindMask] {
		if !sel[aiKindMask].selects(row.Name) {
			continue
		}
		rules, ruleNames := libraryMasks(row)
		if len(rules) == 0 {
			continue
		}
		if storedMasksErr != nil {
			// Appending would replace a list nothing here can read.
			return cfg, fmt.Errorf("the sidecar's configuration rule_library.mask is not a JSON list "+
				"of rules: %v", storedMasksErr)
		}
		names := libraryEntryNames(row.Name, ruleNames)
		for i, fields := range rules {
			if err := claim(aiKindMask, names[i]); err != nil {
				return cfg, err
			}
			fields["name"], _ = json.Marshal(names[i])
			raw, err := json.Marshal(fields)
			if err != nil {
				return cfg, fmt.Errorf("failed rendering data masking rule %q for the rule library: %w", row.Name, err)
			}
			masks = append(masks, raw)
			added = true
		}
		expand[aiKindMask][row.Name] = names
	}

	if len(guardrails) > len(lib.Guardrails) || added {
		lib.Guardrails = guardrails
		if added {
			raw, err := json.Marshal(masks)
			if err != nil {
				return cfg, fmt.Errorf("failed rendering the data masking rule library: %w", err)
			}
			lib.Mask = raw
		}
		cfg.RuleLibrary = &lib
	}
	cfg.Listeners = rewriteLevelRuleNames(cfg.Listeners, expand)
	return cfg, nil
}

// rewriteLevelRuleNames replaces each library row a level names with the
// names its rules are served under. A name with no served rule stays, so the
// sidecar warns about it. The listeners are copied before any change: they
// can share their backing array with the stored row.
func rewriteLevelRuleNames(listeners []daemon.ListenerConfig, expand [len(aiRuleKinds)]map[string][]string) []daemon.ListenerConfig {
	var out []daemon.ListenerConfig
	for i, l := range listeners {
		if l.Analyzer == nil {
			continue
		}
		block := *l.Analyzer
		changed := false
		for _, lv := range analyzerLevels(&block) {
			if !appliesAIRules(*lv.level) {
				continue
			}
			for k, kind := range aiRuleKinds {
				opt := kind.options(lv.level)
				if *opt == nil || len((*opt).Rules) == 0 {
					continue
				}
				names := make([]string, 0, len((*opt).Rules))
				differs := false
				for _, n := range (*opt).Rules {
					served, ok := expand[k][n]
					if !ok {
						names = append(names, n)
						continue
					}
					if len(served) != 1 || served[0] != n {
						differs = true
					}
					names = append(names, served...)
				}
				if !differs {
					continue
				}
				o := **opt
				o.Rules = names
				*opt = &o
				changed = true
			}
		}
		if !changed {
			continue
		}
		if out == nil {
			out = slices.Clone(listeners)
		}
		out[i].Analyzer = &block
	}
	if out == nil {
		return listeners
	}
	return out
}

// ValidateAnalyzerAIRules refuses an analyzer rule whose levels let the AI
// apply guardrails or data masking rules when the sidecar could not run them
// as the admin wrote them. It runs on every save, bound or not:
//
//   - the sidecar's own check of the block (an unknown mode);
//   - options on a level whose action applies no rule (block, defer), which
//     the sidecar ignores;
//   - an organization without an Enterprise license that grants the feature;
//   - a rule name that no library rule with a sidecar block has.
//
// A spec that does not decode is left to the binding checks, as before.
func ValidateAnalyzerAIRules(db *gorm.DB, orgID, ruleName string, spec json.RawMessage) error {
	var block daemon.LaneAnalyzerConfig
	if decodeSpec(spec, &block) != nil {
		return nil
	}
	var used [len(aiRuleKinds)]bool
	var named [len(aiRuleKinds)][]string
	var problems []string
	for _, lv := range analyzerLevels(&block) {
		levelUses := false
		for k, kind := range aiRuleKinds {
			o := *kind.options(lv.level)
			if o == nil {
				continue
			}
			levelUses = true
			used[k] = true
			for _, n := range o.Rules {
				if !slices.Contains(named[k], n) {
					named[k] = append(named[k], n)
				}
			}
		}
		if levelUses && !appliesAIRules(*lv.level) {
			problems = append(problems, fmt.Sprintf("analyzer rule %q: %s risk sets guardrails or mask, and "+
				"its action %q applies no AI rule; set them on an allow, warn, require_approval or "+
				"ai-handoff level", ruleName, lv.name, lv.level.Action))
		}
	}
	if !used[aiKindGuardrails] && !used[aiKindMask] {
		return nil
	}
	if err := ValidateSidecarRuleSpec(SidecarRuleAnalyzer, ruleName, spec); err != nil {
		return err
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}

	licenseData, err := models.GetOrgLicenseData(db, orgID)
	if err != nil {
		return fmt.Errorf("%w: reading the organization license: %v", ErrSidecarRulesUnavailable, err)
	}
	for k, kind := range aiRuleKinds {
		if used[k] && !licenseGrants(licenseData, kind.feature) {
			return fmt.Errorf("analyzer rule %q lets the AI apply %s rules, which needs an Enterprise "+
				"license that grants %s; add one in Settings -> License or contact our support at "+
				"https://help.hoop.dev", ruleName, kind.label, kind.feature)
		}
	}

	org, err := uuid.Parse(orgID)
	if err != nil {
		return fmt.Errorf("%w: parsing the organization id: %v", ErrSidecarRulesUnavailable, err)
	}
	for k, kind := range aiRuleKinds {
		if len(named[k]) == 0 {
			continue
		}
		rows, err := kind.library(db, org)
		if err != nil {
			return fmt.Errorf("%w: reading the %s rule library: %v", ErrSidecarRulesUnavailable, kind.label, err)
		}
		known := map[string]bool{}
		for _, row := range rows {
			switch k {
			case aiKindGuardrails:
				known[row.Name] = len(libraryGuardrails(row)) > 0
			case aiKindMask:
				rules, _ := libraryMasks(row)
				known[row.Name] = len(rules) > 0
			}
		}
		for _, n := range named[k] {
			if !known[n] {
				return fmt.Errorf("analyzer rule %q: %s.rules names %q, which is not a %s rule of this "+
					"organization with sidecar rules; create it first or remove the name",
					ruleName, kind.key, n, kind.label)
			}
		}
	}
	return nil
}

// licenseGrants is the predicate the guardrail and data masking APIs gate
// their saves on: the organization's license is Enterprise. The document was
// verified when it was uploaded (PUT /orgs/license). It also needs the
// feature, as the sidecar does (daemon aiRuleLicenseProblems).
func licenseGrants(licenseData json.RawMessage, feature string) bool {
	if len(licenseData) == 0 {
		return false
	}
	var l commonlicense.License
	if json.Unmarshal(licenseData, &l) != nil {
		return false
	}
	return l.Payload.Type == commonlicense.EnterpriseType && l.IsFeatureEnabled(feature)
}
