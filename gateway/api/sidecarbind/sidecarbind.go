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
	// until the write cascades them.
	Name, StoredName string
	// Spec is the rule in the sidecar's own vocabulary. Nil means the request
	// did not mention it.
	Spec json.RawMessage
	// Targets is the listener set. Nil means the request did not mention it,
	// which LEAVES THE BINDINGS ALONE; an empty slice unbinds everywhere.
	Targets *[]openapi.SidecarRuleTarget
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
	targets := toModelTargets(req.Targets)

	// Is this rule distributed at all? A rule with no binding is stored and
	// reaches nobody, which is a valid state, and it is not checked against a
	// sidecar it never touches.
	bound := len(targets) > 0
	for _, name := range []string{req.Name, req.StoredName} {
		if bound || name == "" {
			continue
		}
		existing, err := boundSidecars(req.Kind, org, name)
		if err != nil {
			abort(c, err)
			return true
		}
		bound = len(existing) > 0
	}
	if !bound {
		return false
	}

	if err := services.ValidateSidecarRuleSpec(req.Kind, req.Name, req.Spec); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return true
	}
	err = services.ValidateSidecarRuleTargets(models.DB, orgID, req.Kind, req.Name, req.Spec, targets)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return true
	}
	return false
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
	switch kind {
	case services.SidecarRuleGuardrail:
		return models.SetGuardrailRuleListeners(models.DB, org, ruleName, toModelTargets(targets))
	case services.SidecarRuleMask:
		return models.SetDataMaskingRuleListeners(models.DB, org, ruleName, toModelTargets(targets))
	case services.SidecarRuleAnalyzer:
		return models.SetAnalyzerRuleListeners(models.DB, org, ruleName, toModelTargets(targets))
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

func boundSidecars(kind services.SidecarRuleKind, orgID uuid.UUID, ruleName string) ([]string, error) {
	switch kind {
	case services.SidecarRuleGuardrail:
		return models.SidecarsBoundToGuardrailRule(models.DB, orgID, ruleName)
	case services.SidecarRuleMask:
		return models.SidecarsBoundToDataMaskingRule(models.DB, orgID, ruleName)
	case services.SidecarRuleAnalyzer:
		return models.SidecarsBoundToAnalyzerRule(models.DB, orgID, ruleName)
	}
	return nil, fmt.Errorf("unknown sidecar rule kind %q", kind)
}

func toModelTargets(in *[]openapi.SidecarRuleTarget) []models.SidecarRuleTarget {
	if in == nil {
		return nil
	}
	out := make([]models.SidecarRuleTarget, 0, len(*in))
	for _, t := range *in {
		if t.SidecarID == "" {
			continue
		}
		out = append(out, models.SidecarRuleTarget{SidecarID: t.SidecarID, ListenerName: t.ListenerName})
	}
	return out
}

func abort(c *gin.Context, err error) {
	log.Errorf("failed reading the rule's sidecar bindings, reason=%v", err)
	c.JSON(http.StatusInternalServerError, gin.H{"message": "failed reading the rule's sidecar bindings"})
}
