package services

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/policy"
	"gorm.io/gorm"
)

// The write gate for a rule the control plane distributes to sidecars.
//
// Every refusal here exists because the alternative is silent. A rule the UI
// accepts and no sidecar can run looks identical to a working one: the fleet
// reports itself healthy, the rule enforces nothing, and nobody learns until
// the statement it was written for goes through. Worse, a rule a sidecar
// REFUSES takes down that sidecar's whole configuration at its next restart,
// so a saved rule can crash-loop a fleet hours after the save.
//
// Nothing here is a second opinion about what a sidecar accepts. The
// authority is the sidecar's own code -- policy.ValidateRules,
// policy.ValidateForSSH, daemon.ValidateSSHMasking and the daemon's own types
// under a strict decode -- which is why those are exported rather than
// reimplemented. A list kept here would pass its own tests and still refuse
// the wrong thing the first time a rule type is added to the daemon.

// SidecarRuleKind names which of the three blocks a spec is.
type SidecarRuleKind string

const (
	SidecarRuleGuardrail SidecarRuleKind = "guardrail"
	SidecarRuleMask      SidecarRuleKind = "data masking"
	SidecarRuleAnalyzer  SidecarRuleKind = "analyzer"
)

// ValidateSidecarRuleSpec checks a stored block on its own, before anything
// about where it is bound.
//
// Two passes, and both are the sidecar's: a strict decode into the daemon's
// own type, which refuses a key no sidecar declares, and then the daemon's own
// semantic check on what decoded.
func ValidateSidecarRuleSpec(kind SidecarRuleKind, ruleName string, spec json.RawMessage) error {
	if len(spec) == 0 || string(spec) == "null" {
		return fmt.Errorf("%s rule %q carries no sidecar configuration; a rule bound to a listener "+
			"has to say what it does there", kind, ruleName)
	}
	switch kind {
	case SidecarRuleGuardrail:
		var block daemon.GuardrailsConfig
		if err := decodeSpec(spec, &block); err != nil {
			return fmt.Errorf("%s rule %q: %w", kind, ruleName, err)
		}
		if len(block.Rules) == 0 {
			return fmt.Errorf("guardrail rule %q has no rules, so binding it to a listener "+
				"would enforce nothing", ruleName)
		}
		// hasScanner is true: a released sidecar links a detector, so a pii
		// rule is valid there. A build with none reports that separately.
		if err := policy.ValidateRules(block.Rules, true); err != nil {
			return fmt.Errorf("guardrail rule %q: %w", ruleName, err)
		}
	case SidecarRuleMask:
		var block struct {
			Rules []maskRule `json:"rules"`
		}
		if err := decodeSpec(spec, &block); err != nil {
			return fmt.Errorf("%s rule %q: %w", kind, ruleName, err)
		}
		if len(block.Rules) == 0 {
			return fmt.Errorf("data masking rule %q has no rules, so binding it to a listener "+
				"would mask nothing", ruleName)
		}
		for i, r := range block.Rules {
			if err := r.validate(); err != nil {
				return fmt.Errorf("data masking rule %q, entry %d: %w", ruleName, i+1, err)
			}
		}
	case SidecarRuleAnalyzer:
		var block daemon.LaneAnalyzerConfig
		if err := decodeSpec(spec, &block); err != nil {
			return fmt.Errorf("%s rule %q: %w", kind, ruleName, err)
		}
		if err := validateAnalyzerBlock(ruleName, block); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown sidecar rule kind %q", kind)
	}
	return nil
}

// ValidateSidecarRuleTargets refuses a binding that does not name exactly one
// listener on a sidecar this organization owns, or a listener whose protocol
// cannot run the rule.
//
// The protocol check is the half a rule editor cannot do on its own. An ssh
// lane refuses `table`, `http_resource`, `http_status` and `grpc_status`
// rules, and every masking strategy but `mask`; an analyzer block needs the
// lane's own analyzer enabled. All four are startup refusals on the sidecar,
// which means a rule saved without this check bricks a fleet at its next
// restart rather than at the save.
func ValidateSidecarRuleTargets(db *gorm.DB, orgID string, kind SidecarRuleKind, ruleName string, spec json.RawMessage, targets []models.SidecarRuleTarget) error {
	seen := map[string]*models.Sidecar{}
	for _, t := range targets {
		sc, ok := seen[t.SidecarID]
		if !ok {
			var err error
			sc, err = models.GetSidecarByNameOrID(db, orgID, t.SidecarID)
			if err != nil {
				return fmt.Errorf("sidecar %q was not found in this organization", t.SidecarID)
			}
			seen[t.SidecarID] = sc
		}
		// A rule binds to one listener, never to a sidecar as a whole. The
		// sidecar-wide binding would write the top-level block, which a lane
		// carrying its own mask block silently replaces -- the same rule
		// applying on some lanes and ignored on others, with nothing saying
		// which. The listener is also what carries the protocol, and this
		// function's whole second half needs it.
		if t.ListenerName == "" {
			return fmt.Errorf("a rule bound to sidecar %q names no listener; bind it to the "+
				"listeners that must enforce it, because a listener's protocol decides which "+
				"rules it can carry", sc.Name)
		}
		var lane *daemon.ListenerConfig
		matches := 0
		for i, l := range sc.Configuration.Listeners {
			if l.Name == t.ListenerName {
				matches++
				lane = &sc.Configuration.Listeners[i]
			}
		}
		if matches != 1 {
			return fmt.Errorf("sidecar %q has %d listeners named %q; a rule must name exactly one",
				sc.Name, matches, t.ListenerName)
		}
		if err := validateSpecForLane(kind, ruleName, spec, sc.Name, *lane); err != nil {
			return err
		}
		if err := checkCapWithRule(db, orgID, sc, kind, ruleName, spec, t.ListenerName); err != nil {
			return err
		}
	}
	return nil
}

// checkCapWithRule refuses a binding that would push a sidecar past the free
// tier's rule cap.
//
// The cap counts what the SERVED document authors, so a rule bound here raises
// a count the stored configuration's own check never saw. Without this the
// save succeeds, the running sidecar refuses the document and keeps its old
// rules, and every pod that reschedules afterwards -- a helm upgrade, a node
// drain -- hard-exits at boot, all at once, hours later.
//
// It composes what the document WOULD be and asks the daemon's own counter, so
// there is no second arithmetic to keep in step with it.
func checkCapWithRule(db *gorm.DB, orgID string, sc *models.Sidecar, kind SidecarRuleKind, ruleName string, spec json.RawMessage, listenerName string) error {
	licenseData, err := models.GetOrgLicenseData(db, sc.OrgID)
	if err != nil {
		// A missing org is the caller's problem, not this check's: the write
		// itself fails on the same row a moment later.
		return nil
	}
	composed, err := ComposeSidecarConfiguration(db, sc)
	if err != nil {
		// Composition is already broken for a reason this binding did not
		// cause. Reporting it here would name the wrong rule.
		return nil
	}
	// The rule being written is not in the database yet, or is there with its
	// old content, so it is folded in by hand on top.
	bound := []models.BoundRule{{RuleName: ruleName, ListenerName: listenerName, Spec: spec}}
	var withRule daemon.Config
	switch kind {
	case SidecarRuleGuardrail:
		withRule, err = foldSidecarRules(composed, bound, nil, nil)
	case SidecarRuleMask:
		withRule, err = foldSidecarRules(composed, nil, bound, nil)
	default:
		// The analyzer is not a rule and is not capped: its controls are the
		// trigger and the call budget rather than a number of rules.
		return nil
	}
	if err != nil {
		return nil
	}
	if err := CheckSidecarConfigurationLimits(withRule, licenseData); err != nil {
		return fmt.Errorf("binding %s rule %q to listener %q on sidecar %q puts that sidecar over "+
			"its rule limit: %w", kind, ruleName, listenerName, sc.Name, err)
	}
	return nil
}

// validateSpecForLane runs the refusals that depend on which lane the rule
// lands on. Everything here is a startup refusal on the sidecar.
func validateSpecForLane(kind SidecarRuleKind, ruleName string, spec json.RawMessage, sidecarName string, lane daemon.ListenerConfig) error {
	where := fmt.Sprintf("listener %q on sidecar %q", lane.Name, sidecarName)
	isSSH := strings.EqualFold(lane.Protocol, "ssh")

	switch kind {
	case SidecarRuleGuardrail:
		if !isSSH {
			return nil
		}
		var block daemon.GuardrailsConfig
		if err := decodeSpec(spec, &block); err != nil {
			return fmt.Errorf("guardrail rule %q: %w", ruleName, err)
		}
		// The daemon's own list of what an ssh lane cannot read.
		if problems := policy.ValidateForSSH(block.Rules); len(problems) > 0 {
			return fmt.Errorf("guardrail rule %q cannot run on %s: %s",
				ruleName, where, strings.Join(problems, "; "))
		}
	case SidecarRuleMask:
		if !isSSH {
			return nil
		}
		var block struct {
			Rules json.RawMessage `json:"rules"`
		}
		if err := decodeSpec(spec, &block); err != nil {
			return fmt.Errorf("data masking rule %q: %w", ruleName, err)
		}
		// An ssh lane rewrites a byte stream in place, so only a
		// length-preserving strategy can run there. The daemon owns that list.
		problems := daemon.ValidateSSHMasking(daemon.MaskConfig{Rules: block.Rules}, where)
		if len(problems) > 0 {
			return fmt.Errorf("data masking rule %q: %s", ruleName, strings.Join(problems, "; "))
		}
	case SidecarRuleAnalyzer:
		// A lane without its analyzer enabled has no provider and no call
		// budget. The sidecar refuses a config whose listener carries an
		// analyzer block with no top-level analyzer section, and a lane the
		// operator never opted in has neither.
		if lane.Analyzer == nil {
			return fmt.Errorf("analyzer rule %q is bound to %s, which has the analyzer turned "+
				"off; enable it on that listener first, so it carries a trigger and a call budget",
				ruleName, where)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// The two shapes the daemon keeps opaque
// ---------------------------------------------------------------------------

// maskRule is the alcatraz rule shape.
//
// Declared here rather than imported because daemon.MaskConfig.Rules is raw
// JSON on purpose: the shape belongs to whichever detector plugin is linked,
// and neither the daemon nor this package links one. daemon.validateSSHMasking
// reads two of these fields for exactly the same reason and says so.
//
// The fields are hoop.dev/docs/features/data-masking. A field added to the
// plugin and not here is refused by the strict decode above, which is the
// failure mode to want: a rule an admin could save and no sidecar could run is
// worse than a rule the API refuses while this struct catches up.
type maskRule struct {
	Name     string   `json:"name,omitempty"`
	Entities []string `json:"entities,omitempty"`
	Columns  []string `json:"columns,omitempty"`
	Strategy string   `json:"strategy,omitempty"`
	KeepLast *int     `json:"keep_last,omitempty"`
	MaskChar *int32   `json:"mask_char,omitempty"`
}

// maskStrategies is the plugin's own list. Empty means redact.
var maskStrategies = map[string]bool{"": true, "redact": true, "mask": true, "partial": true, "hash": true}

func (r maskRule) validate() error {
	if len(r.Entities) == 0 && len(r.Columns) == 0 {
		return fmt.Errorf("names neither entities nor columns, so it would mask nothing")
	}
	if !maskStrategies[r.Strategy] {
		return fmt.Errorf("uses strategy %q; a sidecar masks with redact, mask, partial or hash",
			r.Strategy)
	}
	if r.KeepLast != nil && *r.KeepLast < 0 {
		return fmt.Errorf("sets keep_last to %d", *r.KeepLast)
	}
	return nil
}

// analyzerActions is the lane block's own vocabulary. It is NOT the gateway's
// (allow_execution, block_execution, require_access_request): the two features
// share a name and nothing else.
var analyzerActions = map[string]bool{"": true, "allow": true, "warn": true, "block": true, "defer": true}

func validateAnalyzerBlock(ruleName string, block daemon.LaneAnalyzerConfig) error {
	named := false
	for _, tier := range []struct {
		name   string
		action string
	}{
		{"high", block.HighRisk},
		{"medium", block.MediumRisk},
		{"low", block.LowRisk},
	} {
		if !analyzerActions[tier.action] {
			return fmt.Errorf("analyzer rule %q sets %s risk to %q; a sidecar answers with allow, "+
				"warn, block or defer", ruleName, tier.name, tier.action)
		}
		if tier.action != "" {
			named = true
		}
	}
	if !named {
		// The sidecar's own refusal, quoted: a block naming no action allows
		// every verdict, which is a classifier nobody is reading and a bill
		// nobody approved.
		return fmt.Errorf("analyzer rule %q names no action for any risk level, so every verdict "+
			"would allow while still paying for the classification", ruleName)
	}
	if block.ApprovalRule != "" {
		return fmt.Errorf("analyzer rule %q names an approval rule, which needs the review action; "+
			"a sidecar declares that action in its configuration and refuses it at startup until "+
			"EVL-289 lands. Use block or defer", ruleName)
	}
	return nil
}
