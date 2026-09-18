package apiai

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
)

// refuseSidecarTargets validates an analyzer rule against what a sidecar
// listener can run, and reports whether it answered the request.
//
// The analyzer is the feature with the most distance between the two engines,
// so this refuses more than its guardrail and masking counterparts. A gateway
// rule can ask for a review the sidecar declares but does not yet implement
// (EVL-289), and it can be bound to a lane with no analyzer block at all,
// which has neither a provider nor a trigger. Both refuse the sidecar's whole
// configuration at startup, so a rule saved here would take down every sidecar
// that reschedules -- the control plane manufacturing the divergence it exists
// to report.
//
// It runs on every write to a bound rule, not only when the binding is
// created: editing a compliant rule into a non-compliant one would otherwise
// walk straight past it.
func refuseSidecarTargets(c *gin.Context, orgID uuid.UUID, rule *models.AISessionAnalyzerRules, reqTargets *[]openapi.SidecarRuleTarget) bool {
	targets := toSidecarTargets(derefTargets(reqTargets))

	bound := len(targets) > 0
	if !bound {
		// Already bound elsewhere: the rule's content still has to stay
		// runnable, even when this request does not mention the bindings.
		existing, err := models.SidecarsBoundToAnalyzerRule(models.DB, orgID, rule.Name)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed reading the rule's sidecar bindings")
			return true
		}
		bound = len(existing) > 0
	}
	if !bound {
		return false
	}

	err := services.ValidateAnalyzerRuleForSidecar(rule.Name, rule.RiskEvaluation)
	if err == nil {
		err = services.ValidateSidecarRuleTargets(models.DB, orgID.String(), targets)
	}
	if err == nil {
		err = services.ValidateAnalyzerTargetListeners(models.DB, orgID.String(), rule.Name, targets)
	}
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return true
	}
	return false
}

// persistSidecarTargets replaces the rule's target set. Called only after the
// rule row exists, so the junction's foreign key has something to point at.
//
// An ABSENT field is not an empty one: it leaves the bindings alone, so a write
// that says nothing about sidecars changes nothing about them. An explicit []
// is the admin unbinding the rule, and does replace the set with nothing.
func persistSidecarTargets(orgID uuid.UUID, ruleName string, targets *[]openapi.SidecarRuleTarget) error {
	if targets == nil {
		return nil
	}
	return models.SetAnalyzerRuleListeners(models.DB, orgID, ruleName, toSidecarTargets(*targets))
}

// derefTargets reads the optional field as a list, for the guards, which treat
// "not mentioned" and "none" the same: neither adds a binding, and a rule that
// is bound elsewhere is checked either way.
func derefTargets(in *[]openapi.SidecarRuleTarget) []openapi.SidecarRuleTarget {
	if in == nil {
		return nil
	}
	return *in
}

func toSidecarTargets(in []openapi.SidecarRuleTarget) []models.SidecarRuleTarget {
	out := make([]models.SidecarRuleTarget, 0, len(in))
	for _, t := range in {
		if t.SidecarID == "" {
			continue
		}
		out = append(out, models.SidecarRuleTarget{SidecarID: t.SidecarID, ListenerName: t.ListenerName})
	}
	return out
}

// loadSidecarTargets renders back where a rule is bound, so a round-trip
// through the API returns what was saved.
func loadSidecarTargets(orgID uuid.UUID, ruleName string) []openapi.SidecarRuleTarget {
	rows, err := models.ListAnalyzerRuleTargets(models.DB, orgID, ruleName)
	if err != nil {
		log.Warnf("failed reading the sidecar targets of analyzer rule %q, reason=%v", ruleName, err)
		return nil
	}
	out := make([]openapi.SidecarRuleTarget, 0, len(rows))
	for _, r := range rows {
		out = append(out, openapi.SidecarRuleTarget{SidecarID: r.SidecarID, ListenerName: r.ListenerName})
	}
	return out
}
