package daemon

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hoophq/hoop/sidecar/policy"
)

// The rule counts this build enforces.
//
// They are CONSTANTS, and the distinction from a config field is the whole
// design. A limit an operator can raise in the same file it limits is not a
// limit, it is documentation with extra steps: the person the cap applies to
// is exactly the person holding the editor. So the number lives in the
// binary, and raising it means forking and rebuilding.
//
// That is not a security boundary and must not be described as one. The
// source is public; anyone who wants these rules gone can delete this file
// and run `go build`. What the cap does is mark the edge of the free tier at
// the moment an operator crosses it, in the place they are already reading —
// startup output — rather than after a support conversation. Treat it the
// same way as the require_review refusal in analyzer/evaluator.go: a
// capability this build declines to provide, refused by name, with the
// alternative in the message.
//
// They mirror the control-plane free tier, which caps the same two things at
// the same number per organization (gateway/api/guardrails/guardrails.go and
// gateway/api/datamasking/datamasking.go). One product, one limit, two
// engines.
//
// There is no way to lift them in this build. Adding one — a signed license
// file, most likely, reusing the "guardrails" and "data-masking" feature keys
// that common/license already defines — is a change to this file, not a
// config field.
const (
	maxGuardrailRules = 1
	maxMaskRules      = 1
)

// LimitsSummary renders the caps as the one line -validate prints.
//
// Exported because both entry points report a validated config and neither
// should learn the numbers by hand: sidecar/cmd through Main, and the CLI
// through `hoop start sidecar --validate`.
func LimitsSummary() string {
	return fmt.Sprintf("limits: %d guardrail rule(s), %d data masking rule(s)",
		maxGuardrailRules, maxMaskRules)
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

// checkLimits counts what the config AUTHORS and refuses a config that
// exceeds this build's caps.
//
// Authored, not resolved, and the difference is not cosmetic. resolve()
// concatenates the top-level guardrails.rules into every lane, so counting
// resolved lanes reports one global rule N times and refuses a two-lane
// config that holds exactly one rule. Counting what is written also gives an
// operator a number they can find in their own file.
//
// The cap is therefore per PROCESS rather than per lane, which matches the
// control plane's per-organization cap. A lane-scoped cap would let one
// process hold one rule per listener, and a config with eight lanes is not
// the free tier by any reading. Running eight processes still is, and nothing
// here pretends otherwise.
//
// Returns a problem per violation rather than an error, so it composes with
// the collecting validators either side of it: every problem in one run,
// never one error per restart.
func (c *Config) checkLimits() []string {
	var problems []string

	if sites, total := c.guardrailSites(); total > maxGuardrailRules {
		problems = append(problems, fmt.Sprintf(
			"%d guardrail rules are configured (%s) and this build enforces at most %d; "+
				"merge them into one rule, or contact our support at https://help.hoop.dev. "+
				"ai_analysis rules are counted separately and are not limited",
			total, renderSites(sites), maxGuardrailRules))
	}

	sites, total, errs := c.maskSites()
	problems = append(problems, errs...)
	if total > maxMaskRules {
		problems = append(problems, fmt.Sprintf(
			"%d data masking rules are configured (%s) and this build enforces at most %d; "+
				"one rule may name several columns or one entity, so two rules can often "+
				"become one. Contact our support at https://help.hoop.dev",
			total, renderSites(sites), maxMaskRules))
	}

	return problems
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
