package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
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
		// The daemon's own check, which is the authority: the risk
		// vocabulary, the send mode, the numeric bounds, and the pairing a
		// hold needs -- require_review on a level and an approval rule naming
		// who may release the statement, each refused without the other.
		//
		// postgres stands in for the lane, because this pass has none. Every
		// refusal but one is protocol-independent; the exception is the hold,
		// which needs a database lane, so a holdable protocol here lets the
		// pairing answer and validateSpecForLane asks the real protocol once
		// the rule names a listener.
		where := fmt.Sprintf("analyzer rule %q", ruleName)
		if problems := daemon.ValidateLaneAnalyzerBlock(&block, where, "postgres"); len(problems) > 0 {
			return errors.New(strings.Join(problems, "; "))
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
// storedName is the name the rule's bindings currently sit under, so the
// composed check can leave the version already in the database out. A rename
// makes it differ from ruleName; empty means the rule is new.
func ValidateSidecarRuleTargets(db *gorm.DB, orgID string, kind SidecarRuleKind, ruleName, storedName string, spec json.RawMessage, targets []models.SidecarRuleTarget) error {
	seen := map[string]*models.Sidecar{}
	order := []string{}
	candidates := map[string][]models.BoundRule{}
	for _, t := range targets {
		sc, ok := seen[t.SidecarID]
		if !ok {
			var err error
			sc, err = models.GetSidecarByNameOrID(db, orgID, t.SidecarID)
			if err != nil {
				return fmt.Errorf("sidecar %q was not found in this organization", t.SidecarID)
			}
			seen[t.SidecarID] = sc
			order = append(order, t.SidecarID)
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
		candidates[t.SidecarID] = append(candidates[t.SidecarID], models.BoundRule{
			RuleName: ruleName, ListenerName: t.ListenerName, Spec: spec,
		})
	}
	if kind == SidecarRuleAnalyzer && len(targets) > 0 {
		if err := checkApprovalRuleExists(db, orgID, ruleName, spec); err != nil {
			return err
		}
	}
	// One composition per sidecar, carrying EVERY binding this write adds to
	// it. Per target would count a rule bound to three lanes as one rule, three
	// times over, and never see the cap the third one breaks.
	for _, id := range order {
		if err := checkComposedWithRule(db, seen[id], kind, ruleName, storedName, candidates[id]); err != nil {
			return err
		}
	}
	return nil
}

// checkApprovalRuleExists refuses a hold whose approval rule is not there.
//
// The sidecar cannot see this. It validates that the rule is NAMED and files
// the review; the plane then authorizes that review against its own rows, and
// a name matching nothing is refused there -- once per held statement, at the
// moment a developer is waiting, with the statement denied and no review for
// anyone to approve. The admin's side is silent: the rule saved, the fleet
// applied it, and the reviewer list they meant to point at is one they never
// created or have since renamed.
//
// Access type as well as existence, because that is the whole of what makes a
// rule a sidecar rule, and authorizedApprovalRule refuses on it too.
func checkApprovalRuleExists(db *gorm.DB, orgID, ruleName string, spec json.RawMessage) error {
	var block daemon.LaneAnalyzerConfig
	if err := decodeSpec(spec, &block); err != nil {
		return fmt.Errorf("analyzer rule %q: %w", ruleName, err)
	}
	if block.ApprovalRule == "" {
		return nil
	}
	// The rule's own pair, which this write creates beside it
	// (SyncAnalyzerApprovalRule). Checking it would refuse the save that is
	// about to make it, on every first save of a hold.
	if block.ApprovalRule == ruleName {
		return nil
	}
	org, err := uuid.Parse(orgID)
	if err != nil {
		return fmt.Errorf("%w: parsing the organization id: %v", ErrSidecarRulesUnavailable, err)
	}
	approval, err := models.GetAccessRequestRuleByName(db, block.ApprovalRule, org)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return fmt.Errorf("analyzer rule %q holds statements under approval rule %q, and this "+
			"organization has no access request rule by that name; create it first, so there "+
			"is someone to release a held statement", ruleName, block.ApprovalRule)
	case err != nil:
		return fmt.Errorf("%w: reading approval rule %q: %v",
			ErrSidecarRulesUnavailable, block.ApprovalRule, err)
	case approval.AccessType != models.AccessTypeSidecar:
		return fmt.Errorf("analyzer rule %q names approval rule %q, which is a %q rule; a "+
			"sidecar review is released by a rule whose access type is %q",
			ruleName, block.ApprovalRule, approval.AccessType, models.AccessTypeSidecar)
	}
	return nil
}

// checkComposedWithRule builds the document this write would make the sidecar
// serve, and refuses a write the sidecar could not run.
//
// Two different refusals come out of the one composition, which is why they
// are one function.
//
// The cap: it counts what the SERVED document authors, so a rule bound here
// raises a count the stored configuration's own check never saw. Without this
// the save succeeds, the running sidecar refuses the document and keeps its
// old rules, and every pod that reschedules afterwards -- a helm upgrade, a
// node drain -- hard-exits at boot, all at once, hours later.
//
// The conflict: a listener runs ONE analyzer block, so a second analyzer rule
// bound to a lane that already has one is a document composition cannot build
// at all. Unrefused here it is worse than a bad rule: the handshake fails for
// the WHOLE sidecar, every other rule on it stops being delivered, and the
// fleet sits on its last good document until somebody finds the binding.
//
// It composes what the document WOULD be and asks the daemon's own counter, so
// there is no second arithmetic to keep in step with it.
func checkComposedWithRule(db *gorm.DB, sc *models.Sidecar, kind SidecarRuleKind, ruleName, storedName string, candidates []models.BoundRule) error {
	licenseData, err := models.GetOrgLicenseData(db, sc.OrgID)
	if err != nil {
		return fmt.Errorf("%w: reading the license of the organization owning sidecar %q: %v",
			ErrSidecarRulesUnavailable, sc.Name, err)
	}
	// Composed with the rule's own stored version left out and the incoming
	// one folded in its place: an edit would otherwise count the old content
	// and the new one as two rules and refuse itself at a cap it does not move.
	withRule, err := composeSidecarConfiguration(db, sc, kind, storedName, candidates...)
	if err != nil {
		if errors.Is(err, ErrSidecarRulesUnavailable) {
			return err
		}
		return fmt.Errorf("binding %s rule %q to sidecar %q: %w", kind, ruleName, sc.Name, err)
	}
	if kind == SidecarRuleAnalyzer {
		// Not capped. A lane's analyzer controls are its trigger and its call
		// budget, not a number of rules.
		return nil
	}
	if err := CheckSidecarConfigurationLimits(withRule, licenseData); err != nil {
		return fmt.Errorf("binding %s rule %q to sidecar %q puts that sidecar over its rule "+
			"limit: %w", kind, ruleName, sc.Name, err)
	}
	return nil
}

// ValidateSidecarBindingsForConfiguration refuses a configuration edit that
// would break a rule already bound to this sidecar.
//
// Listener names are the binding key, so removing a listener, renaming one, or
// giving one a protocol its bound rules cannot run all break the NEXT
// handshake rather than this request -- and they break it for the whole
// sidecar, not for the rule: composition returns an error, the handshake
// answers it, and every rule on every other lane stops being delivered too.
// The sidecar keeps its last good document and looks healthy while an admin
// edits rules that no longer reach it.
//
// Run inside the write's own transaction, over the document the write would
// store, so there is no window where the configuration is saved and the
// bindings are not checked.
// ErrSidecarBindingBroken is a configuration edit a rule already bound to this
// sidecar cannot survive. The admin's to fix -- unbind the rule, or keep the
// listener -- so it reads 422 rather than 500.
type ErrSidecarBindingBroken struct{ Reason string }

func (e ErrSidecarBindingBroken) Error() string { return e.Reason }

func ValidateSidecarBindingsForConfiguration(db *gorm.DB, sc *models.Sidecar) error {
	orgID, err := uuid.Parse(sc.OrgID)
	if err != nil {
		return fmt.Errorf("%w: parsing the sidecar organization id: %v", ErrSidecarRulesUnavailable, err)
	}
	lanes := map[string]int{}
	for _, l := range sc.Configuration.Listeners {
		lanes[l.Name]++
	}
	for _, kind := range []SidecarRuleKind{SidecarRuleGuardrail, SidecarRuleMask, SidecarRuleAnalyzer} {
		bound, err := listBoundRules(db, kind, orgID, sc.ID)
		if err != nil {
			return fmt.Errorf("%w: reading the %s rules bound to sidecar %q: %v",
				ErrSidecarRulesUnavailable, kind, sc.Name, err)
		}
		for _, b := range bound {
			if lanes[b.ListenerName] != 1 {
				return ErrSidecarBindingBroken{Reason: fmt.Sprintf("%s rule %q is bound to "+
					"listener %q, which this configuration has %d of; unbind the rule first, "+
					"or keep exactly one listener by that name",
					kind, b.RuleName, b.ListenerName, lanes[b.ListenerName])}
			}
			var lane daemon.ListenerConfig
			for _, l := range sc.Configuration.Listeners {
				if l.Name == b.ListenerName {
					lane = l
					break
				}
			}
			if err := validateSpecForLane(kind, b.RuleName, b.Spec, sc.Name, lane); err != nil {
				return ErrSidecarBindingBroken{Reason: err.Error()}
			}
		}
	}
	return nil
}

func listBoundRules(db *gorm.DB, kind SidecarRuleKind, orgID uuid.UUID, sidecarID string) ([]models.BoundRule, error) {
	switch kind {
	case SidecarRuleGuardrail:
		return models.ListGuardrailRulesForSidecar(db, orgID, sidecarID)
	case SidecarRuleMask:
		return models.ListDataMaskingRulesForSidecar(db, orgID, sidecarID)
	case SidecarRuleAnalyzer:
		return models.ListAnalyzerRulesForSidecar(db, orgID, sidecarID)
	}
	return nil, fmt.Errorf("unknown sidecar rule kind %q", kind)
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
		var block daemon.LaneAnalyzerConfig
		if err := decodeSpec(spec, &block); err != nil {
			return fmt.Errorf("analyzer rule %q: %w", ruleName, err)
		}
		// The half only the lane can answer. A hold denies the first attempt
		// and releases an identical retry, so it needs a client that sends the
		// statement again: a database client does when the developer runs the
		// query once more, an http caller is a program reading a refusal, and
		// an ssh session is a shell the denial already ended.
		// where is already inside each problem, so the rule name is all this
		// adds: the admin is looking at a rule, not at a listener.
		if problems := daemon.ValidateLaneAnalyzerBlock(&block, where, lane.Protocol); len(problems) > 0 {
			return fmt.Errorf("analyzer rule %q: %s", ruleName, strings.Join(problems, "; "))
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
