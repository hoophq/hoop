// Package sidecarbind is the write path shared by the three rule APIs a
// control plane distributes to sidecars.
//
// Guardrails, data masking and the AI analyzer differ in what a rule SAYS and
// not at all in how it is bound, checked and delivered, so the binding lives
// here once instead of three times. The kind parameter is the only thing that
// varies, and it selects the junction table and the validator.
//
// It is the control plane's path. In gateway mode these fields are refused
// rather than stored: a gateway has no sidecars, and a rule silently carrying
// a sidecar block would be a shape nothing reads and nothing maintains.
package sidecarbind

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
)

// Request is the part of a rule request this package reads. The three openapi
// request types carry these two fields under the same names.
type Request struct {
	Kind services.SidecarRuleKind
	// Name the rule is written under, and StoredName the name it currently
	// has. A rename makes them differ, and the bindings sit under the old one
	// until the write cascades them. StoredName is EMPTY on a create, which is
	// what tells the cap check there is no stored version of this rule to leave
	// out of its count.
	Name, StoredName string
	// Spec is the rule in the sidecar's own vocabulary. Nil means the request
	// did not mention it, and StoredSpec stands in for it; an explicit JSON
	// null clears it, which a bound rule is then refused for.
	Spec json.RawMessage
	// StoredSpec is what the rule carries in the database today, so a write
	// that says nothing about the sidecar block keeps it rather than erasing
	// it. Empty on a create.
	StoredSpec json.RawMessage
	// Targets is the listener set. Nil means the request did not mention it,
	// which LEAVES THE BINDINGS ALONE; an empty slice unbinds everywhere.
	Targets *[]openapi.SidecarRuleTarget
}

// EffectiveSpec is the sidecar block this write leaves on the rule.
//
// Omission preserves, for the same reason it does for the targets: a form that
// edits a description, a connection list or an attribute sends the fields it
// owns, and reading its silence as "clear the sidecar block" would unbind a
// fleet's policy from a screen that never mentioned sidecars. An explicit null
// still clears, which is how the block is removed on purpose.
//
// The handler persists THIS value, not req.Spec. Validating one document and
// storing another is how a rule gets saved that no sidecar can run.
func (r Request) EffectiveSpec() json.RawMessage {
	if r.Spec == nil {
		return r.StoredSpec
	}
	return r.Spec
}

// Refuse validates a rule against what a sidecar can run and reports whether
// it answered the request.
//
// It runs on every write to a bound rule, not only when the binding is
// created: editing a compliant rule into a non-compliant one would otherwise
// walk straight past it. And it runs before the rule row is written, so a
// refused rule is not half-saved.
func Refuse(c *gin.Context, orgID string, req Request) bool {
	if !appconfig.Get().IsControlPlane() {
		// Point of order rather than a check: a gateway has no sidecars, so a
		// request carrying either field is a client aimed at the wrong
		// deployment. Storing it silently would leave a column nothing reads.
		if req.Spec != nil || req.Targets != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "sidecar_spec and " +
				"sidecar_targets are control plane fields; this deployment is a gateway and " +
				"has no sidecars to distribute a rule to"})
			return true
		}
		return false
	}

	org, err := uuid.Parse(orgID)
	if err != nil {
		abort(c, err)
		return true
	}

	targets, err := effectiveTargets(req.Targets, func() ([]models.SidecarRuleTarget, error) {
		return storedTargets(req.Kind, org, bindingName(req))
	})
	if err != nil {
		// A malformed list is the admin's; a stored list that would not read
		// is ours. Both refuse, and the second one says so.
		if _, bad := err.(malformedTargets); bad {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		} else {
			abort(c, err)
		}
		return true
	}
	// A rule bound nowhere is stored and reaches nobody, which is a valid
	// state and not one to check against a sidecar it never touches. It is
	// also the state an admin unbinding a broken rule is trying to reach, so
	// refusing it here would leave them unable to.
	if len(targets) == 0 {
		return false
	}

	spec := req.EffectiveSpec()
	if err := services.ValidateSidecarRuleSpec(req.Kind, req.Name, spec); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return true
	}
	err = services.ValidateSidecarRuleTargets(models.DB, orgID, req.Kind, req.Name, req.StoredName, spec, targets)
	switch {
	case err == nil:
		return false
	case errors.Is(err, services.ErrSidecarRulesUnavailable):
		// The check could not run. It still refuses -- a gate that passes when
		// it could not read is the failure it exists to prevent -- but it says
		// 500, because there is nothing in the form for the admin to fix.
		abort(c, err)
	default:
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
	}
	return true
}

// effectiveTargets is where the rule will be bound AFTER this write, which is
// what the write has to be valid against.
//
// A request that names targets replaces the set. One that does not keeps the
// set the rule already has -- and those bindings go on serving the spec this
// write stores, so they are what it is checked against. Reading the absent
// list as "no targets" instead is an edit that walks past every listener,
// protocol and cap check while the fleet keeps enforcing the result.
//
// stored is a func rather than a slice so the caller does not read the
// junction table on a write that replaces it anyway.
func effectiveTargets(requested *[]openapi.SidecarRuleTarget, stored func() ([]models.SidecarRuleTarget, error)) ([]models.SidecarRuleTarget, error) {
	if requested != nil {
		return toModelTargets(requested)
	}
	return stored()
}

// bindingName is the name the rule's bindings sit under right now. A rename
// makes it differ from the name being written, and the junction rows follow
// the rule row through the foreign key's ON UPDATE CASCADE afterwards.
func bindingName(req Request) string {
	if req.StoredName != "" {
		return req.StoredName
	}
	return req.Name
}

// Persist replaces the rule's target set. Called only after the rule row
// exists, so the junction's foreign key has something to point at.
//
// An ABSENT target list is not an empty one: it leaves the bindings alone, so
// a write that says nothing about sidecars changes nothing about them. An
// explicit [] is the admin unbinding the rule.
func Persist(orgID string, kind services.SidecarRuleKind, ruleName string, targets *[]openapi.SidecarRuleTarget) error {
	if targets == nil || !appconfig.Get().IsControlPlane() {
		return nil
	}
	org, err := uuid.Parse(orgID)
	if err != nil {
		return err
	}
	rows, err := toModelTargets(targets)
	if err != nil {
		return err
	}
	switch kind {
	case services.SidecarRuleGuardrail:
		return models.SetGuardrailRuleListeners(models.DB, org, ruleName, rows)
	case services.SidecarRuleMask:
		return models.SetDataMaskingRuleListeners(models.DB, org, ruleName, rows)
	case services.SidecarRuleAnalyzer:
		return models.SetAnalyzerRuleListeners(models.DB, org, ruleName, rows)
	}
	return fmt.Errorf("unknown sidecar rule kind %q", kind)
}

// Load renders back where a rule is bound, so a round-trip through the API
// returns what was saved. Without it an edit form opens with the picker empty
// and the next save unbinds the rule from every sidecar it reached.
func Load(orgID string, kind services.SidecarRuleKind, ruleName string) []openapi.SidecarRuleTarget {
	if !appconfig.Get().IsControlPlane() {
		return nil
	}
	org, err := uuid.Parse(orgID)
	if err != nil {
		return nil
	}
	var rows []models.SidecarRuleTarget
	switch kind {
	case services.SidecarRuleGuardrail:
		rows, err = models.ListGuardrailRuleTargets(models.DB, org, ruleName)
	case services.SidecarRuleMask:
		rows, err = models.ListDataMaskingRuleTargets(models.DB, org, ruleName)
	case services.SidecarRuleAnalyzer:
		rows, err = models.ListAnalyzerRuleTargets(models.DB, org, ruleName)
	}
	if err != nil {
		log.Warnf("failed reading the sidecar targets of %s rule %q, reason=%v", kind, ruleName, err)
		return nil
	}
	out := make([]openapi.SidecarRuleTarget, 0, len(rows))
	for _, r := range rows {
		out = append(out, openapi.SidecarRuleTarget{SidecarID: r.SidecarID, ListenerName: r.ListenerName})
	}
	return out
}

// storedTargets is where the rule is bound today.
func storedTargets(kind services.SidecarRuleKind, orgID uuid.UUID, ruleName string) ([]models.SidecarRuleTarget, error) {
	if ruleName == "" {
		return nil, nil
	}
	switch kind {
	case services.SidecarRuleGuardrail:
		return models.ListGuardrailRuleTargets(models.DB, orgID, ruleName)
	case services.SidecarRuleMask:
		return models.ListDataMaskingRuleTargets(models.DB, orgID, ruleName)
	case services.SidecarRuleAnalyzer:
		return models.ListAnalyzerRuleTargets(models.DB, orgID, ruleName)
	}
	return nil, fmt.Errorf("unknown sidecar rule kind %q", kind)
}

// malformedTargets is a target list the CLIENT sent wrong, as opposed to one
// that could not be read. The first reads 422, the second 500.
type malformedTargets struct{ reason string }

func (e malformedTargets) Error() string { return e.reason }

// toModelTargets refuses a malformed entry rather than dropping it.
//
// Dropping is what makes it dangerous: a list of nothing but malformed entries
// then reads as an empty list, which is the admin unbinding the rule
// everywhere -- so a client bug that sends a target with no sidecar_id deletes
// a fleet's bindings and answers 200.
func toModelTargets(in *[]openapi.SidecarRuleTarget) ([]models.SidecarRuleTarget, error) {
	if in == nil {
		return nil, nil
	}
	out := make([]models.SidecarRuleTarget, 0, len(*in))
	for i, t := range *in {
		if t.SidecarID == "" {
			return nil, malformedTargets{fmt.Sprintf("sidecar target %d names no sidecar; "+
				"every target is one listener on one sidecar", i+1)}
		}
		out = append(out, models.SidecarRuleTarget{SidecarID: t.SidecarID, ListenerName: t.ListenerName})
	}
	return out, nil
}

func abort(c *gin.Context, err error) {
	log.Errorf("failed reading the rule's sidecar bindings, reason=%v", err)
	c.JSON(http.StatusInternalServerError, gin.H{"message": "failed reading the rule's sidecar bindings"})
}
