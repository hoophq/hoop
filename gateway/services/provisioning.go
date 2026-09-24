package services

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"gorm.io/gorm"
)

// Provisioning writes the users and groups a source describes into users and
// user_groups (ADR-0019). The Slack import and the file import go through
// here, and so will SCIM, so a Slack approval reads the same rows whichever
// wrote them.

// maxGroupNameLength is the width of user_groups.name and
// review_groups.group_name.
const maxGroupNameLength = 100

var (
	// ErrProvisionedGroupExists answers a create or rename to a group name
	// another provisioned group already has.
	ErrProvisionedGroupExists = errors.New("a group with this name is already provisioned")
	// ErrAmbiguousEmail refuses to guess which of several hoop users with one
	// email the source means.
	ErrAmbiguousEmail = errors.New("more than one user has this email")
	// ErrProvisionedUserEmailRequired refuses a user with nothing to be
	// matched by: a Slack approval finds the approver by email.
	ErrProvisionedUserEmailRequired = errors.New("the user has no email and its user name is not an email")
	// ErrReservedGroupName refuses a provisioned group named like one hoop
	// grants its own powers to. A source that could write the admin group
	// could make anyone an administrator, or take it away from everyone.
	ErrReservedGroupName = errors.New("the group name is reserved by hoop")
	// ErrInvalidGroupName refuses an empty group name, or one longer than
	// user_groups.name holds.
	ErrInvalidGroupName = errors.New("the group name must have 1 to 100 characters")
)

// IsReservedGroupName reports whether name is one of hoop's own groups:
// admin (ADMIN_USERNAME), auditor (AUDITOR_USERNAME) or approver. It ignores
// case, so a source cannot get around it with "Admin".
func IsReservedGroupName(name string) bool {
	name = strings.TrimSpace(name)
	for _, reserved := range []string{types.GroupAdmin, types.GroupAuditor, types.GroupApprover} {
		if strings.EqualFold(name, reserved) {
			return true
		}
	}
	return false
}

// ValidateProvisionedGroupName returns the trimmed name, or why a source may
// not write a group called that.
func ValidateProvisionedGroupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "" || len(name) > maxGroupNameLength:
		return "", ErrInvalidGroupName
	case IsReservedGroupName(name):
		return "", fmt.Errorf("%w: %s", ErrReservedGroupName, name)
	}
	return name, nil
}

// ProvisionedUser is a user as the source describes it. SlackID, when set, is
// written to users.slack_id: a Slack import knows it, and it is what a click
// is matched by first.
type ProvisionedUser struct {
	ExternalID string
	UserName   string
	Email      string
	Name       string
	SlackID    string
	Active     bool
}

// email is the address the user is matched by: the source's email, or the
// user name when that is an address, as it is for Okta and Entra ID. It is
// stored lower case, so a login that sends another case finds the same user.
func (u ProvisionedUser) email() string {
	if e := normalizeEmail(u.Email); e != "" {
		return e
	}
	if n := normalizeEmail(u.UserName); strings.Contains(n, "@") {
		return n
	}
	return ""
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// UpsertResult says what UpsertProvisionedUser did, for the audit entry of a
// sync or an import.
type UpsertResult struct {
	UserID  string
	Created bool
}

// UpsertProvisionedUser writes the user and returns its hoop id.
//
// With a userID it updates that user. Without one it looks the user up by the
// provider's id, then by email, and creates it when neither matches. Matching
// by email is what adopts a user who already logged in or was invited: the
// provider takes over the account instead of creating a second one with the
// same email, which a Slack approval would refuse as ambiguous.
//
// A user made inactive loses every provisioned group, so neither a login nor a
// Slack click can act with them.
func UpsertProvisionedUser(tx *gorm.DB, orgID, source, userID string, u ProvisionedUser) (string, error) {
	res, err := UpsertProvisionedUserResult(tx, orgID, source, userID, u)
	return res.UserID, err
}

// UpsertProvisionedUserResult is UpsertProvisionedUser, also reporting
// whether the user was created.
func UpsertProvisionedUserResult(tx *gorm.DB, orgID, source, userID string, u ProvisionedUser) (UpsertResult, error) {
	email := u.email()
	if email == "" {
		return UpsertResult{}, ErrProvisionedUserEmailRequired
	}
	userName := strings.TrimSpace(u.UserName)
	if userName == "" {
		userName = email
	}

	var user *models.User
	switch {
	case userID != "":
		var existing models.User
		if err := tx.Where("org_id = ? AND id = ?", orgID, userID).First(&existing).Error; err != nil {
			return UpsertResult{}, err
		}
		// Another user of the org already holding the email would make the
		// next Slack click ambiguous, so the update is refused instead.
		var others int64
		if err := tx.Model(&models.User{}).
			Where("org_id = ? AND lower(email) = ? AND id <> ?", orgID, email, userID).
			Count(&others).Error; err != nil {
			return UpsertResult{}, err
		}
		if others > 0 {
			return UpsertResult{}, fmt.Errorf("%w: %s", ErrAmbiguousEmail, email)
		}
		user = &existing
	default:
		found, err := findProvisionedUser(tx, orgID, source, u.ExternalID, email)
		if err != nil {
			return UpsertResult{}, err
		}
		user = found
	}

	status := "inactive"
	if u.Active {
		status = "active"
	}
	name := strings.TrimSpace(u.Name)

	created := user == nil
	if user == nil {
		subjectID := u.ExternalID
		if subjectID == "" {
			subjectID = uuid.NewString()
		}
		if name == "" {
			name = email
		}
		// The subject is a placeholder until the user's first login: the
		// login finds the user by email and replaces it with the identity
		// provider's own subject.
		user = &models.User{
			ID:       uuid.NewString(),
			OrgID:    orgID,
			Subject:  fmt.Sprintf("%s|%s", source, subjectID),
			Name:     name,
			Email:    email,
			Verified: true,
			Status:   status,
		}
		if err := tx.Create(user).Error; err != nil {
			return UpsertResult{}, fmt.Errorf("failed creating provisioned user %s: %w", email, err)
		}
	} else {
		updates := map[string]any{"email": email, "status": status}
		if name != "" {
			updates["name"] = name
		}
		if err := tx.Model(&models.User{}).Where("id = ?", user.ID).Updates(updates).Error; err != nil {
			return UpsertResult{}, fmt.Errorf("failed updating provisioned user %s: %w", email, err)
		}
	}

	var externalID *string
	if u.ExternalID != "" {
		externalID = &u.ExternalID
	}
	if err := models.UpsertDirectoryUser(tx, &models.DirectoryUser{
		UserID:     user.ID,
		OrgID:      orgID,
		Source:     source,
		ExternalID: externalID,
		UserName:   userName,
	}); err != nil {
		return UpsertResult{}, fmt.Errorf("failed linking provisioned user %s: %w", email, err)
	}

	if u.SlackID != "" {
		if err := linkSlackID(tx, orgID, user, u.SlackID); err != nil {
			return UpsertResult{}, err
		}
	}

	if !u.Active {
		if err := removeProvisionedGroups(tx, orgID, user.ID); err != nil {
			return UpsertResult{}, err
		}
	}
	return UpsertResult{UserID: user.ID, Created: created}, nil
}

// linkSlackID makes slackID the user's Slack link. The Slack import is the
// authority for it: another user of the org holding the same id loses it, so
// a click never matches two people. Both changes are logged.
func linkSlackID(tx *gorm.DB, orgID string, user *models.User, slackID string) error {
	res := tx.Model(&models.User{}).
		Where("org_id = ? AND slack_id = ? AND id <> ?", orgID, slackID, user.ID).
		Update("slack_id", nil)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		log.With("org", orgID).Infof("slack id %s moved to user %s from %d other user(s)",
			slackID, user.Email, res.RowsAffected)
	}
	if user.SlackID == slackID {
		return nil
	}
	if err := tx.Model(&models.User{}).Where("id = ?", user.ID).Update("slack_id", slackID).Error; err != nil {
		return err
	}
	if user.SlackID != "" {
		log.With("org", orgID).Infof("slack id of user %s changed from %s to %s", user.Email, user.SlackID, slackID)
	}
	return nil
}

func findProvisionedUser(tx *gorm.DB, orgID, source, externalID, email string) (*models.User, error) {
	if externalID != "" {
		link, err := models.GetDirectoryUserByExternalID(tx, orgID, source, externalID)
		switch {
		case err == nil:
			var user models.User
			if err := tx.Where("org_id = ? AND id = ?", orgID, link.UserID).First(&user).Error; err != nil {
				return nil, err
			}
			return &user, nil
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return nil, err
		}
	}

	var users []models.User
	if err := tx.Where("org_id = ? AND lower(email) = ?", orgID, email).Find(&users).Error; err != nil {
		return nil, err
	}
	switch len(users) {
	case 0:
		return nil, nil
	case 1:
		return &users[0], nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrAmbiguousEmail, email)
	}
}

// DeactivateProvisionedUser marks the user inactive and removes them from
// every provisioned group.
func DeactivateProvisionedUser(tx *gorm.DB, orgID, userID string) error {
	res := tx.Model(&models.User{}).Where("org_id = ? AND id = ?", orgID, userID).Update("status", "inactive")
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return removeProvisionedGroups(tx, orgID, userID)
}

func removeProvisionedGroups(tx *gorm.DB, orgID, userID string) error {
	return tx.Exec(`
		DELETE FROM private.user_groups
		WHERE org_id = @org AND user_id = @user
		AND name IN (SELECT display_name FROM private.directory_groups WHERE org_id = @org)`,
		map[string]any{"org": orgID, "user": userID}).Error
}

// CreateProvisionedGroup records a new group. A name another provisioned group
// already has is refused; a name that only exists as a manual group is
// adopted, and from then on the source owns its members.
func CreateProvisionedGroup(tx *gorm.DB, orgID, source, displayName, externalID string) (*models.DirectoryGroup, error) {
	displayName, err := ValidateProvisionedGroupName(displayName)
	if err != nil {
		return nil, err
	}
	_, err = models.GetDirectoryGroupByName(tx, orgID, displayName)
	switch {
	case err == nil:
		return nil, ErrProvisionedGroupExists
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return nil, err
	}
	g := &models.DirectoryGroup{
		ID:          uuid.NewString(),
		OrgID:       orgID,
		Source:      source,
		DisplayName: displayName,
		ExternalID:  optionalString(externalID),
	}
	if err := tx.Create(g).Error; err != nil {
		return nil, fmt.Errorf("failed creating provisioned group %s: %w", displayName, err)
	}
	if err := ensureGroupRow(tx, orgID, displayName); err != nil {
		return nil, err
	}
	return g, nil
}

// EnsureProvisionedGroup returns the group a directory sync reports, creating
// it, or renaming it when the source renamed it since the last run.
func EnsureProvisionedGroup(tx *gorm.DB, orgID, source, displayName, externalID string) (*models.DirectoryGroup, error) {
	displayName, err := ValidateProvisionedGroupName(displayName)
	if err != nil {
		return nil, err
	}
	var existing models.DirectoryGroup
	err = tx.Where("org_id = ? AND source = ? AND external_id = ?", orgID, source, externalID).First(&existing).Error
	switch {
	case err == nil:
		if existing.DisplayName != displayName {
			if err := RenameProvisionedGroup(tx, orgID, &existing, displayName); err != nil {
				return nil, err
			}
		}
		return &existing, nil
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return nil, err
	}
	return CreateProvisionedGroup(tx, orgID, source, displayName, externalID)
}

// RenameProvisionedGroup renames the group everywhere its name is load
// bearing:
//   - the members' rows in user_groups;
//   - the access request rules that name it in reviewers_groups,
//     force_approval_groups, approval_required_groups or skip_review_groups;
//   - the review_groups rows of pending reviews, so a review filed before
//     the rename can still be approved by the group's members.
//
// Settled reviews keep the name they were decided under.
func RenameProvisionedGroup(tx *gorm.DB, orgID string, g *models.DirectoryGroup, newName string) error {
	oldName := g.DisplayName
	if strings.TrimSpace(newName) == oldName {
		return nil
	}
	newName, err := ValidateProvisionedGroupName(newName)
	if err != nil {
		return err
	}
	_, err = models.GetDirectoryGroupByName(tx, orgID, newName)
	switch {
	case err == nil:
		return ErrProvisionedGroupExists
	case !errors.Is(err, gorm.ErrRecordNotFound):
		return err
	}

	args := map[string]any{"org": orgID, "old": oldName, "new": newName}
	for _, stmt := range []string{
		// A user already in a group of the new name keeps that row.
		`DELETE FROM private.user_groups a USING private.user_groups b
		 WHERE a.org_id = @org AND a.name = @old AND b.org_id = @org AND b.name = @new AND a.user_id = b.user_id`,
		`DELETE FROM private.user_groups
		 WHERE org_id = @org AND name = @old AND user_id IS NULL AND service_account_id IS NULL`,
		`UPDATE private.user_groups SET name = @new WHERE org_id = @org AND name = @old`,
		`UPDATE private.access_request_rules
		 SET reviewers_groups = array_replace(reviewers_groups, @old, @new),
			 force_approval_groups = array_replace(force_approval_groups, @old, @new),
			 approval_required_groups = array_replace(approval_required_groups, @old, @new),
			 skip_review_groups = array_replace(skip_review_groups, @old, @new),
			 updated_at = NOW()
		 WHERE org_id = @org AND (@old = ANY(reviewers_groups) OR @old = ANY(force_approval_groups)
			 OR @old = ANY(approval_required_groups) OR @old = ANY(skip_review_groups))`,
		`UPDATE private.review_groups rg SET group_name = @new
		 FROM private.reviews r
		 WHERE rg.review_id = r.id AND r.org_id = @org AND r.status = 'PENDING' AND rg.group_name = @old`,
		`UPDATE private.directory_groups SET display_name = @new, updated_at = NOW()
		 WHERE org_id = @org AND display_name = @old`,
	} {
		if err := tx.Exec(stmt, args).Error; err != nil {
			return fmt.Errorf("failed renaming group %s to %s: %w", oldName, newName, err)
		}
	}
	if err := ensureGroupRow(tx, orgID, newName); err != nil {
		return err
	}
	g.DisplayName = newName
	return nil
}

// DeleteProvisionedGroup removes the group and its members' rows. Rules that
// name it keep the name, so an admin sees what they referenced.
func DeleteProvisionedGroup(tx *gorm.DB, orgID string, g *models.DirectoryGroup) error {
	if err := tx.Exec(`DELETE FROM private.user_groups WHERE org_id = ? AND name = ?`,
		orgID, g.DisplayName).Error; err != nil {
		return err
	}
	return tx.Where("org_id = ? AND id = ?", orgID, g.ID).Delete(&models.DirectoryGroup{}).Error
}

// SetGroupMembers makes userIDs the complete member list of the group.
func SetGroupMembers(tx *gorm.DB, orgID, name string, userIDs []string) error {
	ids, err := orgUserIDs(tx, orgID, userIDs)
	if err != nil {
		return err
	}
	del := tx.Where("org_id = ? AND name = ? AND user_id IS NOT NULL", orgID, name)
	if len(ids) > 0 {
		del = del.Where("user_id NOT IN ?", ids)
	}
	if err := del.Delete(&models.UserGroup{}).Error; err != nil {
		return err
	}
	return insertMembers(tx, orgID, name, ids)
}

func insertMembers(tx *gorm.DB, orgID, name string, ids []string) error {
	for _, id := range ids {
		if err := tx.Exec(`
			INSERT INTO private.user_groups (org_id, user_id, name)
			VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, orgID, id, name).Error; err != nil {
			return err
		}
	}
	return ensureGroupRow(tx, orgID, name)
}

// orgUserIDs keeps the ids that are users of the org. An id the identity
// provider sends for a user it never provisioned is skipped with a warning
// rather than failing the whole request, which would leave every other member
// of the group unwritten.
func orgUserIDs(tx *gorm.DB, orgID string, userIDs []string) ([]string, error) {
	var wanted []string
	for _, id := range userIDs {
		if _, err := uuid.Parse(id); err == nil {
			wanted = append(wanted, id)
		} else {
			log.With("org", orgID).Warnf("skipping group member %q: not a hoop user id", id)
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	var found []string
	if err := tx.Raw(`SELECT id::TEXT FROM private.users WHERE org_id = ? AND id::TEXT IN ?`,
		orgID, wanted).Scan(&found).Error; err != nil {
		return nil, err
	}
	if len(found) != len(wanted) {
		log.With("org", orgID).Warnf("skipping %d group member(s) that are not users of the org",
			len(wanted)-len(found))
	}
	return found, nil
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

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
