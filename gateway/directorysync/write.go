package directorysync

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"gorm.io/gorm"
)

// maxGroupNameLength is the width of user_groups.name and
// review_groups.group_name.
const maxGroupNameLength = 100

var (
	// ErrReservedGroupName refuses a user group named like one hoop grants its
	// own powers to. An import that could write the admin group could make
	// anyone an administrator, or take it away from everyone.
	ErrReservedGroupName = errors.New("the group name is reserved by hoop")
	// ErrAmbiguousUser refuses to guess which of several hoop users a Slack
	// member is.
	ErrAmbiguousUser = errors.New("more than one hoop user matches this Slack user")

	errInvalidGroupName = errors.New("the group name must have 1 to 100 characters")
	errNoEmail          = errors.New("the Slack user has no email")
)

// isReservedGroupName reports whether name is one of hoop's own groups: admin
// (ADMIN_USERNAME), auditor (AUDITOR_USERNAME) or approver. It ignores case,
// so a handle cannot get around it with "Admin".
func isReservedGroupName(name string) bool {
	for _, reserved := range []string{types.GroupAdmin, types.GroupAuditor, types.GroupApprover} {
		if strings.EqualFold(name, reserved) {
			return true
		}
	}
	return false
}

// write makes hoop match Slack for the selected groups, inside the caller's
// transaction.
func write(tx *gorm.DB, orgID string, w *workspace, selected []slackservice.UserGroup, diff *runDiff) error {
	// Who is in an imported group before this run: a user who drops out and
	// is deleted in Slack is deactivated below.
	before, err := importedMembers(tx, orgID)
	if err != nil {
		return err
	}

	userIDs := map[string]string{} // Slack id -> hoop user id
	for _, g := range selected {
		for _, m := range w.members(g) {
			if _, done := userIDs[m.ID]; done {
				continue
			}
			id, created, err := upsertMember(tx, orgID, m)
			if errors.Is(err, errNoEmail) {
				log.With("org", orgID).Warnf("skipping slack user %s: no email", m.ID)
				continue
			}
			if err != nil {
				return fmt.Errorf("slack user %s: %w", m.ID, err)
			}
			userIDs[m.ID] = id
			if created {
				diff.UsersCreated = append(diff.UsersCreated, normalizeEmail(m.Email))
			}
		}
	}

	picked := map[string]bool{}
	for _, g := range selected {
		name, err := ensureGroup(tx, orgID, g)
		if err != nil {
			return err
		}
		picked[g.ID] = true
		var members []string
		for _, m := range w.members(g) {
			if id, ok := userIDs[m.ID]; ok {
				members = append(members, id)
			}
		}
		prev, err := models.ListGroupMemberIDs(tx, orgID, name)
		if err != nil {
			return err
		}
		if err := setGroupMembers(tx, orgID, name, members); err != nil {
			return err
		}
		if err := diff.recordMembership(tx, name, prev, members); err != nil {
			return err
		}
	}

	groups, err := models.ListDirectoryGroups(tx, orgID)
	if err != nil {
		return err
	}
	for _, g := range groups {
		if picked[g.ExternalID] {
			continue
		}
		if err := deleteGroup(tx, orgID, g); err != nil {
			return err
		}
		diff.GroupsRemoved = append(diff.GroupsRemoved, g.DisplayName)
	}

	// A user who left every group keeps the account: leaving a user group is
	// not leaving the company. Only a user Slack deleted is deactivated, and
	// never an admin, so an import cannot lock the org out.
	seen := map[string]bool{}
	for _, id := range userIDs {
		seen[id] = true
	}
	for _, u := range before {
		if seen[u.ID] || u.SlackID == "" || !w.users[u.SlackID].Deleted {
			continue
		}
		admin, err := isAdmin(tx, orgID, u.ID)
		if err != nil {
			return err
		}
		if admin {
			continue
		}
		if err := tx.Model(&models.User{}).Where("org_id = ? AND id = ?", orgID, u.ID).
			Update("status", "inactive").Error; err != nil {
			return err
		}
		diff.UsersDeactivated = append(diff.UsersDeactivated, u.Email)
	}
	return nil
}

type importedMember struct {
	ID      string
	Email   string
	SlackID string
}

// importedMembers returns the active users in any group the import owns.
func importedMembers(tx *gorm.DB, orgID string) ([]importedMember, error) {
	var out []importedMember
	err := tx.Raw(`
		SELECT DISTINCT u.id::TEXT AS id, u.email, COALESCE(u.slack_id, '') AS slack_id
		FROM private.users u
		JOIN private.user_groups ug ON ug.user_id = u.id
		JOIN private.directory_groups dg ON dg.org_id = ug.org_id AND dg.display_name = ug.name
		WHERE u.org_id = ? AND u.status <> 'inactive'
		ORDER BY u.email`, orgID).Scan(&out).Error
	return out, err
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// upsertMember writes the Slack member as a hoop user and returns its id.
//
// It finds the user by Slack ID, then by email, and creates one when neither
// matches. Matching by email adopts a user who already logged in or was
// added by hand, instead of creating a second user with the same email, which
// a Slack click would refuse as ambiguous. The email is stored lower case, so
// a login that sends another case finds the same user.
//
// An existing user keeps their status: the import never reactivates a user an
// admin deactivated.
func upsertMember(tx *gorm.DB, orgID string, m slackservice.DirectoryUser) (string, bool, error) {
	email := normalizeEmail(m.Email)
	if email == "" {
		return "", false, errNoEmail
	}
	name := strings.TrimSpace(m.Name)

	var users []models.User
	if err := tx.Where("org_id = ? AND slack_id = ?", orgID, m.ID).Find(&users).Error; err != nil {
		return "", false, err
	}
	if len(users) == 0 {
		if err := tx.Where("org_id = ? AND lower(email) = ?", orgID, email).Find(&users).Error; err != nil {
			return "", false, err
		}
	}
	switch len(users) {
	case 0:
		if name == "" {
			name = email
		}
		// The subject is a placeholder until the user's first login: the
		// login finds the user by email and replaces it with the identity
		// provider's own subject.
		user := models.User{
			ID:       uuid.NewString(),
			OrgID:    orgID,
			Subject:  "slack|" + m.ID,
			Name:     name,
			Email:    email,
			Verified: true,
			Status:   "active",
			SlackID:  m.ID,
		}
		if err := tx.Create(&user).Error; err != nil {
			return "", false, fmt.Errorf("failed creating user %s: %w", email, err)
		}
		return user.ID, true, nil
	case 1:
	default:
		return "", false, fmt.Errorf("%w: %s", ErrAmbiguousUser, email)
	}

	user := users[0]
	updates := map[string]any{"email": email, "slack_id": m.ID}
	if name != "" {
		updates["name"] = name
	}
	if err := tx.Model(&models.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
		return "", false, fmt.Errorf("failed updating user %s: %w", email, err)
	}
	if user.SlackID != "" && user.SlackID != m.ID {
		log.With("org", orgID).Infof("slack id of user %s changed from %s to %s", email, user.SlackID, m.ID)
	}
	return user.ID, false, nil
}

// ensureGroup returns the hoop name of an imported user group, and records the
// group on its first import. The name is the handle at that first import. It
// does not follow a rename in Slack, so the rules that name it keep working.
//
// A name that only exists as a group set by hand is adopted: from then on the
// import owns its members.
func ensureGroup(tx *gorm.DB, orgID string, g slackservice.UserGroup) (string, error) {
	existing, err := models.GetDirectoryGroup(tx, orgID, g.ID)
	switch {
	case err == nil:
		return existing.DisplayName, nil
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return "", err
	}

	name := strings.TrimSpace(groupName(g))
	switch {
	case name == "" || len(name) > maxGroupNameLength:
		return "", fmt.Errorf("group @%s: %w", name, errInvalidGroupName)
	case isReservedGroupName(name):
		return "", fmt.Errorf("group @%s: %w", name, ErrReservedGroupName)
	}
	_, err = models.GetDirectoryGroupByName(tx, orgID, name)
	switch {
	case err == nil:
		return "", fmt.Errorf("group %s is already imported from another Slack user group", name)
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return "", err
	}
	if err := tx.Create(&models.DirectoryGroup{OrgID: orgID, ExternalID: g.ID, DisplayName: name}).Error; err != nil {
		return "", fmt.Errorf("failed recording group %s: %w", name, err)
	}
	return name, ensureGroupRow(tx, orgID, name)
}

// deleteGroup removes a group that is no longer picked, with its members'
// rows. Rules that name it keep the name, so an admin sees what they
// referenced.
func deleteGroup(tx *gorm.DB, orgID string, g models.DirectoryGroup) error {
	if err := tx.Exec(`DELETE FROM private.user_groups WHERE org_id = ? AND name = ?`,
		orgID, g.DisplayName).Error; err != nil {
		return err
	}
	return tx.Where("org_id = ? AND external_id = ?", orgID, g.ExternalID).Delete(&models.DirectoryGroup{}).Error
}

// setGroupMembers makes userIDs the complete member list of the group.
func setGroupMembers(tx *gorm.DB, orgID, name string, userIDs []string) error {
	del := tx.Where("org_id = ? AND name = ? AND user_id IS NOT NULL", orgID, name)
	if len(userIDs) > 0 {
		del = del.Where("user_id NOT IN ?", userIDs)
	}
	if err := del.Delete(&models.UserGroup{}).Error; err != nil {
		return err
	}
	for _, id := range userIDs {
		if err := tx.Exec(`
			INSERT INTO private.user_groups (org_id, user_id, name)
			VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, orgID, id, name).Error; err != nil {
			return err
		}
	}
	return nil
}

// ensureGroupRow keeps a member-less row for the group, so GET /users/groups
// lists it even while nobody is in it.
func ensureGroupRow(tx *gorm.DB, orgID, name string) error {
	return tx.Exec(`
		INSERT INTO private.user_groups (org_id, name)
		SELECT @org, @name
		WHERE NOT EXISTS (
			SELECT 1 FROM private.user_groups
			WHERE org_id = @org AND name = @name AND user_id IS NULL AND service_account_id IS NULL)`,
		map[string]any{"org": orgID, "name": name}).Error
}

func isAdmin(tx *gorm.DB, orgID, userID string) (bool, error) {
	var admin bool
	err := tx.Raw(`SELECT EXISTS (
		SELECT 1 FROM private.user_groups WHERE org_id = ? AND user_id::TEXT = ? AND name = ?)`,
		orgID, userID, types.GroupAdmin).Scan(&admin).Error
	return admin, err
}
