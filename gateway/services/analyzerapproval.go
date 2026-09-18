package services

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/hoophq/hoop/sidecar/daemon"
	"gorm.io/gorm"
)

// A hold is two objects, and only one of them is a thing an admin asked for.
//
// The analyzer rule says WHEN to hold a statement. An access request rule says
// WHO may release it -- the reviewer groups and how many approvals. The sidecar
// learns the first from the block it is served and names the second by name;
// the control plane resolves that name when the sidecar files a review, and
// refuses the review if it resolves to nothing.
//
// A control plane has no page for the second object. So an admin who switched
// a rule to "hold for approval" and nothing else would save a rule that holds
// every matching statement and can release none of them -- denied on the first
// attempt, denied on the retry, with the review sitting in a list nobody can
// approve. This file is what makes the switch a complete control: the rule is
// created beside the analyzer rule, under the same name, and removed with it.
//
// The reviewer groups are fixed, and one approval releases. There is nowhere
// to configure them and nothing here reads a request field, which is the point:
// the admin flips one switch and the plane owns the rest.

// analyzerApprovalManagedBy marks the rules this file owns.
//
// It is provenance, and it is also the guard on the one destructive path: a
// rule an admin created by hand is never deleted because an analyzer rule
// happened to share its name.
const analyzerApprovalManagedBy = "ai-session-analyzer"

// analyzerApprovalMinApprovals releases a held statement on one approval, from
// either reviewer group. approvableSidecarRule refuses a minimum above the
// number of groups, so this moves with reviewerGroups.
const analyzerApprovalMinApprovals = 1

// reviewerGroups is who may release a held statement.
//
// Resolved, never spelled. types.GroupAdmin is "admin" only by default: it
// follows ADMIN_USERNAME and the server config's AdminRoleName, so an
// organization that renamed the role would get a rule naming a group nobody is
// in -- and every review under it would sit pending forever, which is the
// silent failure this whole path exists to avoid.
func reviewerGroups() []string {
	return []string{types.GroupApprover, types.GroupAdmin}
}

// AnalyzerRuleHolds reports whether a stored analyzer spec asks to hold a
// statement for a human on any risk level.
//
// It reads the same field the sidecar reads. A spec that does not decode is
// not a hold: the write gate refuses it separately, with a message about the
// shape, and answering "yes" here would create an approval rule for a rule
// that is about to be refused.
func AnalyzerRuleHolds(spec json.RawMessage) bool {
	var block daemon.LaneAnalyzerConfig
	if err := decodeSpec(spec, &block); err != nil {
		return false
	}
	for _, action := range []string{block.HighRisk, block.MediumRisk, block.LowRisk} {
		if action == string(analyzerReviewAction) {
			return true
		}
	}
	return false
}

// analyzerReviewAction is the daemon's own spelling, quoted once.
const analyzerReviewAction = "require_review"

// SyncAnalyzerApprovalRule makes the approval rule match what the analyzer rule
// now asks for: present while the rule holds, gone once it stops.
//
// Called inside the caller's transaction, so the two rules commit together. A
// rule that held statements and lost its approval rule halfway would deny
// every matching statement with no way to release one.
func SyncAnalyzerApprovalRule(tx *gorm.DB, orgID uuid.UUID, ruleName string, spec json.RawMessage) error {
	if AnalyzerRuleHolds(spec) {
		return upsertAnalyzerApprovalRule(tx, orgID, ruleName)
	}
	return DeleteAnalyzerApprovalRule(tx, orgID, ruleName)
}

// upsertAnalyzerApprovalRule creates the rule, or refreshes the fields this
// file owns on the one it already made.
//
// A rule of the same name that this file did not create is left ALONE and
// reported. Overwriting it would silently replace whoever an admin chose as
// reviewers for something else with these two groups.
func upsertAnalyzerApprovalRule(tx *gorm.DB, orgID uuid.UUID, ruleName string) error {
	existing, err := models.GetAccessRequestRuleByName(tx, ruleName, orgID)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		managedBy := analyzerApprovalManagedBy
		minApprovals := analyzerApprovalMinApprovals
		description := fmt.Sprintf(
			"Releases statements held by the %q analyzer rule. Managed by that rule.", ruleName)
		return models.CreateAccessRequestRule(tx, &models.AccessRequestRule{
			OrgID:       orgID,
			Name:        ruleName,
			Description: &description,
			AccessType:  models.AccessTypeSidecar,
			ManagedBy:   &managedBy,
			// Empty, and it has to be: a sidecar rule gates no connection, and
			// validateSidecarAccessRequestRuleBody refuses a non-empty list on
			// a control plane. A listener is not a connection.
			ConnectionNames:        []string{},
			ApprovalRequiredGroups: []string{},
			ReviewersGroups:        reviewerGroups(),
			ForceApprovalGroups:    []string{},
			MinApprovals:           &minApprovals,
		})
	case err != nil:
		return fmt.Errorf("failed reading the approval rule for analyzer rule %q: %w", ruleName, err)
	case existing.ManagedBy == nil || *existing.ManagedBy != analyzerApprovalManagedBy:
		return fmt.Errorf("an access request rule named %q already exists and was not created by "+
			"this analyzer rule; rename the analyzer rule, or remove that access request rule "+
			"first", ruleName)
	}

	// Ours: bring the groups back in step. They are resolved from the server's
	// own role names, so a rule written before an admin role was renamed would
	// otherwise keep naming a group nobody is in.
	minApprovals := analyzerApprovalMinApprovals
	existing.ReviewersGroups = reviewerGroups()
	existing.MinApprovals = &minApprovals
	existing.AllGroupsMustApprove = false
	return models.UpdateAccessRequestRule(tx, existing)
}

// DeleteAnalyzerApprovalRule removes the rule this file created for an analyzer
// rule, and nothing else.
//
// Absent is success: the switch was never on, or this already ran. A rule of
// the same name that something else created is left alone -- deleting it would
// take an admin's own reviewer policy with it.
func DeleteAnalyzerApprovalRule(tx *gorm.DB, orgID uuid.UUID, ruleName string) error {
	existing, err := models.GetAccessRequestRuleByName(tx, ruleName, orgID)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil
	case err != nil:
		return fmt.Errorf("failed reading the approval rule for analyzer rule %q: %w", ruleName, err)
	case existing.ManagedBy == nil || *existing.ManagedBy != analyzerApprovalManagedBy:
		return nil
	}
	return models.DeleteAccessRequestRuleByName(tx, ruleName, orgID)
}
