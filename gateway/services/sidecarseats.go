package services

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"

	commonlicense "github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// FreeTierSidecarSeats is how many sidecars an organization without an
// Enterprise license may hold. Its rules are capped separately, per process,
// by the sidecar's own free tier (sidecar/daemon/limits.go).
const FreeTierSidecarSeats = 1

// ErrSidecarSeatLimit is a free organization creating a sidecar past its seats,
// or a sidecar past them handshaking.
var ErrSidecarSeatLimit = errors.New("the free plan allows 1 sidecar per organization; " +
	"remove the existing one, or add an enterprise license in Settings -> License " +
	"(contact our support at https://help.hoop.dev)")

// OrgHasEnterpriseSidecars is the same predicate apiroutes.EnterpriseLicenseOnly
// and /serverinfo use, so the seat cap, the API gate and the UI agree on who is
// free.
func OrgHasEnterpriseSidecars(licenseData json.RawMessage) bool {
	if len(bytes.TrimSpace(licenseData)) == 0 {
		return false
	}
	l, err := commonlicense.Parse(licenseData, appconfig.Get().ApiHostname())
	return err == nil && l.Payload.Type == commonlicense.EnterpriseType
}

// CreateSidecarWithinSeats creates a token sidecar and its listener mirrors
// inside tx, and refuses it when a free organization would hold more than its
// seats. The seats are counted LAST, after the mirrors validate the document,
// so a bad document reads as the bad document and not as a full plan. The
// organization's sidecar lock is taken first, so two concurrent creations
// cannot both see a free seat.
func CreateSidecarWithinSeats(tx *gorm.DB, sidecar *models.Sidecar) error {
	if err := models.LockOrgSidecars(tx, sidecar.OrgID); err != nil {
		return err
	}
	if err := models.CreateSidecar(tx, sidecar); err != nil {
		return err
	}
	if err := SyncSidecarListenerConnectionsTx(tx, sidecar); err != nil {
		return err
	}
	licenseData, err := models.GetOrgLicenseData(tx, sidecar.OrgID)
	if err != nil {
		return err
	}
	if OrgHasEnterpriseSidecars(licenseData) {
		return nil
	}
	n, err := models.CountSidecars(tx, sidecar.OrgID)
	if err != nil {
		return err
	}
	if n > FreeTierSidecarSeats {
		return ErrSidecarSeatLimit
	}
	return nil
}

// CheckSidecarSeat refuses a sidecar of a free organization that is not one of
// its oldest FreeTierSidecarSeats. An organization only gets there by losing
// an Enterprise license with more sidecars than that; the oldest keep serving
// and the rest stop at their next handshake, rather than every one of them
// running free.
func CheckSidecarSeat(db *gorm.DB, orgID, sidecarID string, licenseData json.RawMessage) error {
	if OrgHasEnterpriseSidecars(licenseData) {
		return nil
	}
	ids, err := models.OldestSidecarIDs(db, orgID, FreeTierSidecarSeats)
	if err != nil {
		return err
	}
	if !slices.Contains(ids, sidecarID) {
		return ErrSidecarSeatLimit
	}
	return nil
}
