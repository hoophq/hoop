package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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

// ENG-662: an analyzer block's ai_rules.<level> may let the AI apply rules of
// the organization's guardrail and data masking libraries to one statement.
// The level names them in its guardrails and mask options; composition serves
// them in the document's rule_library.

// aiRuleKind is one feature whose library rules a level may let the AI apply.
type aiRuleKind struct {
	// key is the level field, as the sidecar spells it.
	key string
	// label names the library in a refusal.
	label string
	// feature is the license feature the kind needs.
	feature string
	options func(*daemon.LevelAIRule) **daemon.AIRuleOptions
	library func(*gorm.DB, uuid.UUID) ([]models.LibraryRule, error)
}

var aiRuleKinds = [...]aiRuleKind{
	{
		key: "guardrails", label: "guardrail", feature: commonlicense.FeatureGuardrails,
		options: func(r *daemon.LevelAIRule) **daemon.AIRuleOptions { return &r.Guardrails },
		library: models.ListGuardrailSidecarLibrary,
	},
	{
		key: "mask", label: "data masking", feature: commonlicense.FeatureDataMasking,
		options: func(r *daemon.LevelAIRule) **daemon.AIRuleOptions { return &r.Mask },
		library: models.ListDataMaskingSidecarLibrary,
	},
}

const (
	aiKindGuardrails = 0
	aiKindMask       = 1
)

// levelAIRules are the ai_rules entries an analyzer block sets. The
// sidecar's validation refuses an entry on a level whose action applies no
// AI rule, so every entry here applies.
func levelAIRules(la *daemon.LaneAnalyzerConfig) []*daemon.LevelAIRule {
	var out []*daemon.LevelAIRule
	for _, level := range []analyzer.RiskLevel{analyzer.RiskHigh, analyzer.RiskMedium, analyzer.RiskLow} {
		if r := la.LevelAIRule(level); r != nil {
			out = append(out, r)
		}
	}
	return out
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

// selectAIRules reads the selections of every lane's analyzer ai_rules.
func selectAIRules(cfg daemon.Config) [len(aiRuleKinds)]aiRuleSelection {
	var out [len(aiRuleKinds)]aiRuleSelection
	for _, l := range cfg.Listeners {
		for _, r := range levelAIRules(l.Analyzer) {
			for k, kind := range aiRuleKinds {
				o := *kind.options(r)
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

// libraryEntryNames are the rule_library names of one library row's n
// rules. Row names are free text (no API refuses a character in them), so the
// scheme is injective by construction rather than by a reserved character:
// every '#' of the row name is doubled, a row with one rule serves it under
// that escaped name, and a row with several serves rule i as escaped#i. In
// an escaped name every '#' comes in a pair, so a single '#' followed only by
// digits can come from a multi-rule row and nothing else: "a#1" is rule 1 of
// row "a", and row "a#1" serves "a##1".
func libraryEntryNames(row string, n int) []string {
	esc := strings.ReplaceAll(row, "#", "##")
	if n == 1 {
		return []string{esc}
	}
	out := make([]string, n)
	for i := range out {
		out[i] = esc + "#" + strconv.Itoa(i+1)
	}
	return out
}

// libraryEntry is one rule of a selected library row, before it is named.
type libraryEntry struct {
	row  string
	base string
}

// nameLibraryEntries gives each entry its served name: its base name, unless
// a rule_library rule of the stored configuration (held) already has it.
// Such an entry gets base#dupN, the first N no stored name and no base name
// uses. Every base is reserved before any entry is named, so a renamed entry
// never takes a later entry's name. Nothing here can fail: a name clash must
// not stop the configuration from reaching the sidecar.
func nameLibraryEntries(entries []libraryEntry, held map[string]bool) []string {
	reserved := maps.Clone(held)
	for _, e := range entries {
		reserved[e.base] = true
	}
	assigned := map[string]bool{}
	out := make([]string, len(entries))
	for i, e := range entries {
		name := e.base
		if held[name] || assigned[name] {
			for n := 1; ; n++ {
				c := e.base + "#dup" + strconv.Itoa(n)
				if !reserved[c] && !assigned[c] {
					name = c
					break
				}
			}
		}
		assigned[name] = true
		out[i] = name
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
// object whose shape the detector plugin owns.
func libraryMasks(row models.LibraryRule) []map[string]json.RawMessage {
	var block struct {
		Rules []json.RawMessage `json:"rules"`
	}
	if decodeSpec(row.Spec, &block) != nil {
		return nil
	}
	rules := make([]map[string]json.RawMessage, 0, len(block.Rules))
	for _, raw := range block.Rules {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return nil
		}
		rules = append(rules, fields)
	}
	return rules
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
// The levels' lists are rewritten to the served names (libraryEntryNames,
// nameLibraryEntries). A rule_library the stored configuration writes itself
// is kept with its names; a library rule that would take one of them is
// served under another name, so the result never names a rule twice.
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
	expand := [len(aiRuleKinds)]map[string][]string{{}, {}}

	// Guardrails: the selected rules, then their names.
	var guardEntries []libraryEntry
	var guardRules []policy.Rule
	for _, row := range rows[aiKindGuardrails] {
		if !sel[aiKindGuardrails].selects(row.Name) {
			continue
		}
		rules := libraryGuardrails(row)
		for _, base := range libraryEntryNames(row.Name, len(rules)) {
			guardEntries = append(guardEntries, libraryEntry{row: row.Name, base: base})
		}
		guardRules = append(guardRules, rules...)
	}
	guardrails := slices.Clone(lib.Guardrails)
	for i, name := range nameLibraryEntries(guardEntries, held[aiKindGuardrails]) {
		r := guardRules[i]
		r.Name = name
		guardrails = append(guardrails, r)
		expand[aiKindGuardrails][guardEntries[i].row] = append(expand[aiKindGuardrails][guardEntries[i].row], name)
	}

	// Masks, the same way.
	var maskEntries []libraryEntry
	var maskRules []map[string]json.RawMessage
	for _, row := range rows[aiKindMask] {
		if !sel[aiKindMask].selects(row.Name) {
			continue
		}
		rules := libraryMasks(row)
		for _, base := range libraryEntryNames(row.Name, len(rules)) {
			maskEntries = append(maskEntries, libraryEntry{row: row.Name, base: base})
		}
		maskRules = append(maskRules, rules...)
	}
	if len(maskEntries) > 0 && storedMasksErr != nil {
		// Appending would replace a list nothing here can read; the sidecar
		// refuses that list anyway.
		return cfg, fmt.Errorf("the sidecar's configuration rule_library.mask is not a JSON list "+
			"of rules: %v", storedMasksErr)
	}
	for i, name := range nameLibraryEntries(maskEntries, held[aiKindMask]) {
		fields := maskRules[i]
		fields["name"], _ = json.Marshal(name)
		raw, err := json.Marshal(fields)
		if err != nil {
			return cfg, fmt.Errorf("failed rendering data masking rule %q for the rule library: %w", maskEntries[i].row, err)
		}
		masks = append(masks, raw)
		expand[aiKindMask][maskEntries[i].row] = append(expand[aiKindMask][maskEntries[i].row], name)
	}

	if len(guardEntries) > 0 || len(maskEntries) > 0 {
		lib.Guardrails = guardrails
		if len(maskEntries) > 0 {
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

// rewriteLevelRuleNames replaces each library row an ai_rules entry names
// with the names its rules are served under. A name with no served rule
// stays, so the sidecar warns about it. The listeners, their ai_rules and
// each changed entry are copied before any change: they can share memory
// with the stored row.
func rewriteLevelRuleNames(listeners []daemon.ListenerConfig, expand [len(aiRuleKinds)]map[string][]string) []daemon.ListenerConfig {
	var out []daemon.ListenerConfig
	for i, l := range listeners {
		if l.Analyzer == nil || l.Analyzer.AIRules == nil {
			continue
		}
		rules := *l.Analyzer.AIRules
		changed := false
		for _, slot := range []**daemon.LevelAIRule{&rules.High, &rules.Medium, &rules.Low} {
			if *slot == nil {
				continue
			}
			entry := **slot
			entryChanged := false
			for k, kind := range aiRuleKinds {
				opt := kind.options(&entry)
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
				entryChanged = true
			}
			if entryChanged {
				*slot = &entry
				changed = true
			}
		}
		if !changed {
			continue
		}
		if out == nil {
			out = slices.Clone(listeners)
		}
		block := *l.Analyzer
		block.AIRules = &rules
		out[i].Analyzer = &block
	}
	if out == nil {
		return listeners
	}
	return out
}

// ValidateAnalyzerAIRules refuses an analyzer rule whose ai_rules let the AI
// apply guardrails or data masking rules when the sidecar could not run them
// as the admin wrote them. It runs on every analyzer rule save, bound or not;
// a listener's own analyzer block gets the same checks from
// ValidateListenerAIRules:
//
//   - the sidecar's own check of the block (an entry on a level whose action
//     applies no AI rule, an empty entry, an unknown mode);
//   - an organization without an Enterprise license that grants the feature;
//   - a rule name that no library rule with a sidecar block has, nor, on each
//     sidecar of targets, that sidecar's stored rule_library. Composition
//     serves that rule_library as it is, so an imported rule keeps naming the
//     entries of the file it came from. A rule bound nowhere has only the
//     organization's library.
//
// targets are the bindings the rule has after the save.
// A spec that does not decode is left to the binding checks, as before.
func ValidateAnalyzerAIRules(db *gorm.DB, orgID, ruleName string, spec json.RawMessage, targets []models.SidecarRuleTarget) error {
	var block daemon.LaneAnalyzerConfig
	if decodeSpec(spec, &block) != nil || block.AIRules == nil {
		return nil
	}
	if err := ValidateSidecarRuleSpec(SidecarRuleAnalyzer, ruleName, spec); err != nil {
		return err
	}
	used, named := aiRuleOptionUse(&block)
	if !used[aiKindGuardrails] && !used[aiKindMask] {
		return nil
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
	subject := fmt.Sprintf("analyzer rule %q", ruleName)
	names := newAIRuleNames(db, org, nil)
	if err := names.load(named); err != nil {
		return err
	}
	sidecars := []string{}
	for _, t := range targets {
		if !slices.Contains(sidecars, t.SidecarID) {
			sidecars = append(sidecars, t.SidecarID)
		}
	}
	if len(sidecars) == 0 {
		return names.refuse(subject, named)
	}
	for _, id := range sidecars {
		sc, err := models.GetSidecarByNameOrID(db, orgID, id)
		switch {
		case errors.Is(err, models.ErrNotFound):
			return fmt.Errorf("sidecar %q was not found in this organization", id)
		case err != nil:
			return fmt.Errorf("%w: reading sidecar %q: %v", ErrSidecarRulesUnavailable, id, err)
		}
		on := names.holding(sc.Configuration.RuleLibrary,
			fmt.Sprintf(", nor a rule of the rule_library of sidecar %q", sc.Name))
		if err := on.refuse(subject, named); err != nil {
			return err
		}
	}
	return nil
}

// refuse is unknown as an error: the refusal, or the failed read.
func (n *aiRuleNames) refuse(subject string, named [len(aiRuleKinds)][]string) error {
	u, err := n.unknown(subject, named)
	if err != nil {
		return err
	}
	if u != "" {
		return errors.New(u)
	}
	return nil
}

// ValidateListenerAIRules refuses a configuration write whose listener
// analyzer ai_rules name a rule that neither a library rule with a sidecar
// block nor the document's own rule_library holds. The sidecar's validation
// (CheckSidecarConfiguration, CheckComposedSidecarConfiguration) refuses the
// rest of what ValidateAnalyzerAIRules refuses, and
// CheckSidecarConfigurationLimits the license. A load_from_disk document is
// not served, so it is not checked.
func ValidateListenerAIRules(db *gorm.DB, sc *models.Sidecar) error {
	cfg := daemon.Config(sc.Configuration)
	if cfg.LoadFromDisk != nil && *cfg.LoadFromDisk {
		return nil
	}
	var problems []string
	var names *aiRuleNames
	for _, l := range cfg.Listeners {
		_, named := aiRuleOptionUse(l.Analyzer)
		if len(named[aiKindGuardrails]) == 0 && len(named[aiKindMask]) == 0 {
			continue
		}
		if names == nil {
			org, err := uuid.Parse(sc.OrgID)
			if err != nil {
				return fmt.Errorf("%w: parsing the organization id: %v", ErrSidecarRulesUnavailable, err)
			}
			names = newAIRuleNames(db, org, cfg.RuleLibrary)
		}
		u, err := names.unknown(fmt.Sprintf("listener %q analyzer block", l.Name), named)
		if err != nil {
			return err
		}
		if u != "" {
			problems = append(problems, u)
		}
	}
	if len(problems) > 0 {
		return ErrSidecarAnalyzerInvalid{Reason: strings.Join(problems, "; ")}
	}
	return nil
}

// aiRuleOptionUse reads which kinds an analyzer block's ai_rules use and the
// names they list.
func aiRuleOptionUse(block *daemon.LaneAnalyzerConfig) (used [len(aiRuleKinds)]bool,
	named [len(aiRuleKinds)][]string) {
	for _, r := range levelAIRules(block) {
		for k, kind := range aiRuleKinds {
			o := *kind.options(r)
			if o == nil {
				continue
			}
			used[k] = true
			for _, n := range o.Rules {
				if !slices.Contains(named[k], n) {
					named[k] = append(named[k], n)
				}
			}
		}
	}
	return used, named
}

// aiRuleNames resolves the names a level may list: the organization's library
// rows with a sidecar rule, read once per kind, and the names of a stored
// rule_library (held), which composition serves as they are.
type aiRuleNames struct {
	db    *gorm.DB
	org   uuid.UUID
	held  [len(aiRuleKinds)]map[string]bool
	known [len(aiRuleKinds)]map[string]bool
	// where names the held rule_library in a refusal; empty when none.
	where string
}

func newAIRuleNames(db *gorm.DB, org uuid.UUID, lib *daemon.RuleLibrary) *aiRuleNames {
	n := &aiRuleNames{db: db, org: org}
	where := ""
	if lib != nil {
		where = ", nor a rule of this configuration's rule_library"
	}
	n.hold(lib, where)
	return n
}

// holding is n resolving against lib instead of n's held names. It shares the
// library rows n read, so call load first to read them once.
func (n *aiRuleNames) holding(lib *daemon.RuleLibrary, where string) *aiRuleNames {
	m := &aiRuleNames{db: n.db, org: n.org, known: n.known}
	m.hold(lib, where)
	return m
}

func (n *aiRuleNames) hold(lib *daemon.RuleLibrary, where string) {
	n.held = [len(aiRuleKinds)]map[string]bool{{}, {}}
	n.where = where
	if lib == nil {
		return
	}
	for _, r := range lib.Guardrails {
		n.held[aiKindGuardrails][r.Name] = true
	}
	var masks []struct {
		Name string `json:"name"`
	}
	// A list that does not decode holds no name; the sidecar refuses it.
	if json.Unmarshal(lib.Mask, &masks) == nil {
		for _, m := range masks {
			n.held[aiKindMask][m.Name] = true
		}
	}
}

// load reads the library rows of every kind named lists, once.
func (n *aiRuleNames) load(named [len(aiRuleKinds)][]string) error {
	for k, kind := range aiRuleKinds {
		if len(named[k]) == 0 || n.known[k] != nil {
			continue
		}
		rows, err := kind.library(n.db, n.org)
		if err != nil {
			return fmt.Errorf("%w: reading the %s rule library: %v", ErrSidecarRulesUnavailable, kind.label, err)
		}
		n.known[k] = map[string]bool{}
		for _, row := range rows {
			switch k {
			case aiKindGuardrails:
				n.known[k][row.Name] = len(libraryGuardrails(row)) > 0
			case aiKindMask:
				n.known[k][row.Name] = len(libraryMasks(row)) > 0
			}
		}
	}
	return nil
}

// unknown is the refusal of the first name nothing resolves, or "".
func (n *aiRuleNames) unknown(subject string, named [len(aiRuleKinds)][]string) (string, error) {
	if err := n.load(named); err != nil {
		return "", err
	}
	for k, kind := range aiRuleKinds {
		for _, name := range named[k] {
			if n.known[k][name] || n.held[k][name] {
				continue
			}
			return fmt.Sprintf("%s: ai_rules %s.rules names %q, which is not a %s rule of this "+
				"organization with sidecar rules%s; create it first or remove the name",
				subject, kind.key, name, kind.label, n.where), nil
		}
	}
	return "", nil
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
