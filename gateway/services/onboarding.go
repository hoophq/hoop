package services

import (
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// Reads the checklist and latches whatever is newly satisfied. A failed latch
// is logged, not returned: the status is already correct and the next call
// retries. Steady state is zero writes.
func SyncOrgOnboardingStatus(db *gorm.DB, orgID, adminGroupName string) (*models.OrgOnboardingStatus, error) {
	status, err := models.GetOrgOnboardingStatus(db, orgID, adminGroupName)
	if err != nil {
		return nil, err
	}
	if len(status.NewlySatisfied) > 0 {
		if err := models.LatchOrgOnboardingSteps(db, orgID, status.NewlySatisfied); err != nil {
			log.Warnf("failed latching onboarding steps for org %s, err=%v", orgID, err)
		}
	}
	if status.AllChecksPass() && !status.Completed {
		if err := models.LatchOrgOnboardingCompleted(db, orgID); err != nil {
			// Stay incomplete so the client keeps calling and retries the stamp.
			log.Warnf("failed latching onboarding completion for org %s, err=%v", orgID, err)
		} else {
			status.Completed = true
		}
	}
	return status, nil
}
