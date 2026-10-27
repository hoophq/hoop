package services

import (
	"strings"

	"github.com/hoophq/hoop/gateway/models"
	"gorm.io/gorm"
)

// SidecarPerson is who a sidecar approval request is filed under. Email is
// empty when nothing named a person. The user fields are set when exactly
// one approver user of the org has that email.
type SidecarPerson struct {
	Email   string
	UserID  string
	Name    string
	SlackID string
}

// SidecarPersonEmail picks the email: the principal when it is one, else the
// sidecar's owner, else nothing.
func SidecarPersonEmail(principal string, owner *string) string {
	if p := strings.TrimSpace(principal); strings.Contains(p, "@") {
		return p
	}
	if owner != nil {
		return strings.TrimSpace(*owner)
	}
	return ""
}

// ResolveSidecarPerson picks the email and looks up its hoop user. Two users
// with one email identify nobody: a message to the wrong one is worse than
// none. A lookup failure is returned with the email already set.
func ResolveSidecarPerson(db *gorm.DB, orgID, principal string, owner *string) (SidecarPerson, error) {
	person := SidecarPerson{Email: SidecarPersonEmail(principal, owner)}
	if person.Email == "" {
		return person, nil
	}
	users, err := models.ListApproverUsersByEmailAndOrg(db, orgID, person.Email)
	if err != nil {
		return person, err
	}
	if len(users) != 1 {
		return person, nil
	}
	person.UserID = users[0].ID
	person.Name = users[0].Name
	person.SlackID = users[0].SlackID
	return person, nil
}
