package daemon

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/policy"
)

// The rule counts an UNLICENSED process enforces, mirroring the control
// plane's free tier. Constants, not config keys: a limit the file it limits
// can raise is documentation with extra steps. Only a license Hoop signed
// moves them. Not a security boundary; the source is public.
const (
	maxGuardrailRules = 1
	maxMaskRules      = 1
)

// unlimited is the cap a licensed feature runs under. Negative rather than a
// large number, so a comparison that forgets to test for it fails loudly
// instead of imposing a cap nobody wrote.
const unlimited = -1

// caps are the rule counts this process may author, after the license has
// had its say. A struct because the check, the summary line, the startup log
// and the admin endpoint all read the pair, and a call site holding one of
// them can report a cap it does not enforce.
type caps struct {
	guardrails int
	mask       int
}

// capsFor reads a license into the caps it lifts, per feature: a customer who
// bought data masking does not get unlimited guardrails with it. Status
// grants nothing unless the license verified and is an enterprise one, so an
// oss license and an expired one both land here as the free tier.
func capsFor(lic license.Status) caps {
	c := caps{guardrails: maxGuardrailRules, mask: maxMaskRules}
	if lic.Allows(license.FeatureGuardrails) {
		c.guardrails = unlimited
	}
	if lic.Allows(license.FeatureDataMasking) {
		c.mask = unlimited
	}
	return c
}

// exceeds reports whether n is over a cap. An unlimited cap is over nothing.
func exceeds(n, limit int) bool { return limit != unlimited && n > limit }

// capText renders a cap for a human.
func capText(n int) string {
	if n == unlimited {
		return "unlimited"
	}
	return strconv.Itoa(n)
}

// capJSON renders a cap for the admin endpoint. Unlimited is null, the one
// JSON value meaning "no number applies"; a sentinel reads as a cap of minus
// one to somebody else's dashboard.
func capJSON(n int) *int {
	if n == unlimited {
		return nil
	}
	return &n
}

// LimitsSummary renders the caps as the one line -validate prints. Exported
// because both entry points report a validated config and neither should
// learn the numbers by hand: sidecar/cmd through Main, the CLI through
// `hoop start sidecar --validate`.
func LimitsSummary(lic license.Status) string {
	c := capsFor(lic)
	return fmt.Sprintf("limits: %s guardrail rule(s), %s data masking rule(s)",
		capText(c.guardrails), capText(c.mask))
}

// ruleSite is one place in the config that authors rules, named the way the
// operator wrote it.
//
// The count alone is useless in a file with a defaults block and five lanes:
// "6 rules, at most 1" sends someone reading from the top, and the top is
// usually the one rule they meant to keep. Carrying the per-site breakdown
// costs a slice and turns the message into a map of what to merge.
type ruleSite struct {
	name  string
	count int
}

// checkLimits counts what the config AUTHORS, not what each lane resolves:
// resolve() copies top-level rules into every lane, so counting resolved
// lanes would refuse a two-lane config holding one rule. The cap is per
// PROCESS, matching the control plane's per-organization one.
func (c *Config) checkLimits(lic license.Status) []string {
	var problems []string
	limit := capsFor(lic)

	if sites, total := c.guardrailSites(); exceeds(total, limit.guardrails) {
		problems = append(problems, fmt.Sprintf(
			"%d guardrail rules are configured (%s) and this process enforces at most %d; "+
				"merge them into one rule.%s "+
				"ai_analysis rules are counted separately and are not limited",
			total, renderSites(sites), limit.guardrails, licenseAdvice(lic)))
	}

	sites, total, errs := c.maskSites()
	problems = append(problems, errs...)
	if exceeds(total, limit.mask) {
		problems = append(problems, fmt.Sprintf(
			"%d data masking rules are configured (%s) and this process enforces at most %d; "+
				"one rule may name several columns or one entity, so two rules can often "+
				"become one.%s",
			total, renderSites(sites), limit.mask, licenseAdvice(lic)))
	}

	return problems
}

// licenseAdvice says what would lift the cap. Hitting one is the moment an
// operator looks straight at the limit, and "contact our support" wastes it
// when the reason is a license that expired last week.
func licenseAdvice(lic license.Status) string {
	switch lic.State {
	case license.StateExpired:
		return " " + licenseName(lic) + " expired, so the free tier caps are back in " +
			"force; renew it at " + license.Support + "."
	case license.StateValid:
		return " " + licenseName(lic) + " does not cover this; ask about it at " +
			license.Support + "."
	default:
		return " A license lifts this cap: add one with the license flag, the " +
			license.EnvVar + " environment variable, or the \"license\" key in the config " +
			"file. Contact our support at " + license.Support + "."
	}
}

// licenseName says which license a message is about. Naming the source is the
// difference between an operator editing the right file and editing three.
func licenseName(lic license.Status) string {
	if lic.Source == "" {
		return "This license"
	}
	return "The license from " + lic.Source
}

// guardrailSites counts the non-ai_analysis rules each guardrails block
// authors.
//
// Only the canonical `guardrails` spelling is read. The deprecated `policy`
// block folds onto it in normalize, which every loader runs before anything
// validates, so counting both would double every rule written the old way.
//
// ai_analysis rules are excluded because they are not guardrails: they leave
// the process, they cost money per statement, and their own controls are the
// trigger and the max_calls budget rather than a count. Capping them here
// would also cap them by accident, since both kinds share one slice.
func (c *Config) guardrailSites() ([]ruleSite, int) {
	var sites []ruleSite
	total := 0

	add := func(name string, gc *GuardrailsConfig) {
		if gc == nil {
			return
		}
		n := 0
		for _, r := range gc.Rules {
			if r.Type != policy.MatchAIAnalysis {
				n++
			}
		}
		if n > 0 {
			sites = append(sites, ruleSite{name, n})
			total += n
		}
	}

	add("guardrails", c.Guardrails)
	for i, lc := range c.Listeners {
		add(lc.displayName(i), lc.Guardrails)
	}
	return sites, total
}

// maskSites counts the rules each mask block authors, and reports any block
// whose rules are not a JSON array.
//
// The decode is the only way to get a count: MaskConfig.Rules is raw JSON so
// the daemon does not link a detector, and its shape belongs to the plugin.
// Reporting a malformed block here rather than leaving it to BuildMasker
// covers the case the plugin never sees — a broken top-level block that every
// lane overrides is decoded by nothing and would load as a silent zero.
func (c *Config) maskSites() (sites []ruleSite, total int, problems []string) {
	// site names the block in the breakdown, where names it in an error.
	// They differ because the breakdown reads as a list of places to merge
	// ("mask: 5, appdb: 4") and the error reads as a path to a typo
	// ("appdb: mask.rules: ...").
	add := func(site, where string, mc *MaskConfig) {
		if mc == nil {
			return
		}
		n, err := countMaskRules(mc.Rules)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", where, err))
			return
		}
		if n > 0 {
			sites = append(sites, ruleSite{site, n})
			total += n
		}
	}

	add("mask", "mask.rules", c.Mask)
	for i, lc := range c.Listeners {
		name := lc.displayName(i)
		add(name, name+": mask.rules", lc.Mask)
	}
	return sites, total, problems
}

// countMaskRules reports how many rules a raw mask block holds.
//
// It exists because `len(mc.Rules) == 0` measures BYTES, not rules:
// MaskConfig.Rules is a json.RawMessage, so a list holding three rules and a
// list holding one are both "non-empty" to a length test. hasRules answers
// on/off from the same bytes; this answers how many, which is what a cap
// needs.
//
// Decoding into []json.RawMessage rather than the plugin's rule type keeps
// the daemon ignorant of the shape, which is the reason the field is raw in
// the first place. Element-level validation stays with the plugin.
func countMaskRules(raw json.RawMessage) (int, error) {
	if len(raw) == 0 {
		return 0, nil
	}
	var rules []json.RawMessage
	if err := json.Unmarshal(raw, &rules); err != nil {
		return 0, fmt.Errorf("not a JSON array of rules: %w", err)
	}
	return len(rules), nil
}

// renderSites turns the per-block counts into "guardrails: 1, appdb: 3".
func renderSites(sites []ruleSite) string {
	parts := make([]string, 0, len(sites))
	for _, s := range sites {
		parts = append(parts, fmt.Sprintf("%s: %d", s.name, s.count))
	}
	return strings.Join(parts, ", ")
}
