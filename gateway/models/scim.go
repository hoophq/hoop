package models

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SCIMToken is an organization's active SCIM 2.0 bearer token record.
type SCIMToken struct {
	ID        string    `gorm:"column:id"`
	OrgID     string    `gorm:"column:org_id"`
	TokenHash string    `gorm:"column:token_hash"`
	CreatedBy string    `gorm:"column:created_by"`
	CreatedAt time.Time `gorm:"column:created_at"`
	RotatedAt time.Time `gorm:"column:rotated_at"`
}

func GetSCIMTokenByOrg(db *gorm.DB, orgID string) (*SCIMToken, error) {
	var tok SCIMToken
	err := db.Table("private.scim_tokens").Where("org_id = ?", orgID).First(&tok).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &tok, err
}

func GetSCIMTokenByHash(db *gorm.DB, tokenHash string) (*SCIMToken, error) {
	var tok SCIMToken
	err := db.Table("private.scim_tokens").Where("token_hash = ?", tokenHash).First(&tok).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &tok, err
}

func UpsertSCIMToken(db *gorm.DB, orgID, tokenHash, createdBy string) (*SCIMToken, error) {
	now := time.Now().UTC()
	var tok SCIMToken
	err := db.Raw(`
		INSERT INTO private.scim_tokens (id, org_id, token_hash, created_by, created_at, rotated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (org_id) DO UPDATE
		SET token_hash = EXCLUDED.token_hash, created_by = EXCLUDED.created_by, rotated_at = EXCLUDED.rotated_at
		RETURNING id, org_id, token_hash, created_by, created_at, rotated_at`,
		uuid.NewString(), orgID, tokenHash, createdBy, now, now).Scan(&tok).Error
	if err != nil {
		return nil, err
	}
	return &tok, nil
}

func DeleteSCIMToken(db *gorm.DB, orgID string) error {
	return db.Exec(`DELETE FROM private.scim_tokens WHERE org_id = ?`, orgID).Error
}

// SCIMUser is the projection of private.users served and updated over SCIM 2.0.
type SCIMUser struct {
	ID         string         `gorm:"column:id"`
	OrgID      string         `gorm:"column:org_id"`
	Subject    string         `gorm:"column:subject"`
	Email      string         `gorm:"column:email"`
	Name       string         `gorm:"column:name"`
	Status     string         `gorm:"column:status"`
	ExternalID sql.NullString `gorm:"column:external_id"`
}

func (u SCIMUser) Active() bool { return u.Status == "active" }

// ListSCIMUsers returns matching users of orgID with 1-based startIndex and count.
// filterField may be "", "userName", "email", or "externalId".
func ListSCIMUsers(db *gorm.DB, orgID, filterField, filterVal string, startIndex, count int) ([]SCIMUser, int64, error) {
	if startIndex < 1 {
		startIndex = 1
	}
	if count <= 0 || count > 200 {
		count = 100
	}
	q := db.Table("private.users").Where("org_id = ?", orgID)
	switch filterField {
	case "userName", "email":
		q = q.Where("lower(email) = lower(?)", filterVal)
	case "externalId":
		q = q.Where("external_id = ?", filterVal)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var out []SCIMUser
	err := q.Select("id, org_id, subject, email, name, status, external_id").
		Order("email, id").
		Offset(startIndex - 1).
		Limit(count).
		Find(&out).Error
	return out, total, err
}

func GetSCIMUserByID(db *gorm.DB, orgID, id string) (*SCIMUser, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	var u SCIMUser
	err := db.Table("private.users").
		Select("id, org_id, subject, email, name, status, external_id").
		Where("org_id = ? AND id = ?", orgID, id).
		First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	return &u, err
}

// CreateOrAdoptSCIMUser creates a user in private.users or adopts an existing
// user in orgID with the same email (so a person who already logged in is not
// duplicated). Returns ErrAlreadyExists when externalId conflicts with another user.
func CreateOrAdoptSCIMUser(db *gorm.DB, orgID, email, name, externalID string, active bool) (*SCIMUser, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	status := "active"
	if !active {
		status = "inactive"
	}
	var ext sql.NullString
	if externalID != "" {
		ext = sql.NullString{String: externalID, Valid: true}
	}

	var result SCIMUser
	var created bool
	err := db.Transaction(func(tx *gorm.DB) error {
		if externalID != "" {
			var existing SCIMUser
			err := tx.Table("private.users").
				Select("id, org_id, subject, email, name, status, external_id").
				Where("org_id = ? AND external_id = ?", orgID, externalID).
				First(&existing).Error
			if err == nil {
				return ErrAlreadyExists
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		var existing SCIMUser
		err := tx.Table("private.users").
			Select("id, org_id, subject, email, name, status, external_id").
			Where("org_id = ? AND lower(email) = lower(?)", orgID, email).
			First(&existing).Error
		switch {
		case err == nil:
			if name == "" {
				name = existing.Name
			}
			if err := tx.Exec(`
				UPDATE private.users SET name = ?, status = ?, external_id = COALESCE(?, external_id)
				WHERE org_id = ? AND id = ?`,
				name, status, ext, orgID, existing.ID).Error; err != nil {
				return err
			}
			return tx.Table("private.users").
				Select("id, org_id, subject, email, name, status, external_id").
				Where("org_id = ? AND id = ?", orgID, existing.ID).
				First(&result).Error
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return err
		}

		id := uuid.NewString()
		subject := "scim:" + id
		if err := tx.Exec(`
			INSERT INTO private.users (id, org_id, subject, email, name, verified, status, external_id)
			VALUES (?, ?, ?, ?, ?, true, ?, ?)`,
			id, orgID, subject, email, name, status, ext).Error; err != nil {
			return err
		}
		created = true
		return tx.Table("private.users").
			Select("id, org_id, subject, email, name, status, external_id").
			Where("org_id = ? AND id = ?", orgID, id).
			First(&result).Error
	})
	if err != nil {
		return nil, false, err
	}
	return &result, created, nil
}

func UpdateSCIMUser(db *gorm.DB, orgID, id, email, name string, externalID *string, active bool) (*SCIMUser, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	email = strings.ToLower(strings.TrimSpace(email))
	status := "active"
	if !active {
		status = "inactive"
	}
	var out SCIMUser
	err := db.Transaction(func(tx *gorm.DB) error {
		var cur SCIMUser
		if err := tx.Table("private.users").
			Select("id, org_id, subject, email, name, status, external_id").
			Where("org_id = ? AND id = ?", orgID, id).
			First(&cur).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		ext := cur.ExternalID
		if externalID != nil {
			if *externalID == "" {
				ext = sql.NullString{}
			} else {
				ext = sql.NullString{String: *externalID, Valid: true}
			}
		}
		if email == "" {
			email = cur.Email
		}
		if err := tx.Exec(`
			UPDATE private.users SET email = ?, name = ?, status = ?, external_id = ?
			WHERE org_id = ? AND id = ?`,
			email, name, status, ext, orgID, id).Error; err != nil {
			return err
		}
		return tx.Table("private.users").
			Select("id, org_id, subject, email, name, status, external_id").
			Where("org_id = ? AND id = ?", orgID, id).
			First(&out).Error
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func DeactivateSCIMUser(db *gorm.DB, orgID, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	res := db.Exec(`UPDATE private.users SET status = 'inactive' WHERE org_id = ? AND id = ?`, orgID, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// SCIMGroup is a SCIM 2.0 group backed by private.scim_groups and private.user_groups.
type SCIMGroup struct {
	ID          string            `gorm:"column:id"`
	OrgID       string            `gorm:"column:org_id"`
	ExternalID  sql.NullString    `gorm:"column:external_id"`
	DisplayName string            `gorm:"column:display_name"`
	Members     []SCIMGroupMember `gorm:"-"`
}

type SCIMGroupMember struct {
	UserID string `gorm:"column:user_id"`
	Email  string `gorm:"column:email"`
	Name   string `gorm:"column:name"`
}

func loadSCIMGroupMembers(db *gorm.DB, orgID, displayName string) ([]SCIMGroupMember, error) {
	var members []SCIMGroupMember
	err := db.Raw(`
		SELECT u.id AS user_id, u.email, u.name
		FROM private.user_groups ug
		JOIN private.users u ON u.id = ug.user_id AND u.org_id = ug.org_id
		WHERE ug.org_id = ? AND ug.name = ?
		ORDER BY u.email, u.id`, orgID, displayName).Scan(&members).Error
	return members, err
}

func ListSCIMGroups(db *gorm.DB, orgID, filterField, filterVal string, startIndex, count int) ([]SCIMGroup, int64, error) {
	if startIndex < 1 {
		startIndex = 1
	}
	if count <= 0 || count > 200 {
		count = 100
	}
	q := db.Table("private.scim_groups").Where("org_id = ?", orgID)
	switch filterField {
	case "displayName":
		q = q.Where("lower(display_name) = lower(?)", filterVal)
	case "externalId":
		q = q.Where("external_id = ?", filterVal)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var groups []SCIMGroup
	if err := q.Select("id, org_id, external_id, display_name").
		Order("display_name, id").
		Offset(startIndex - 1).
		Limit(count).
		Find(&groups).Error; err != nil {
		return nil, 0, err
	}
	for i := range groups {
		m, err := loadSCIMGroupMembers(db, orgID, groups[i].DisplayName)
		if err != nil {
			return nil, 0, err
		}
		groups[i].Members = m
	}
	return groups, total, nil
}

func GetSCIMGroupByID(db *gorm.DB, orgID, id string) (*SCIMGroup, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	var g SCIMGroup
	err := db.Table("private.scim_groups").
		Select("id, org_id, external_id, display_name").
		Where("org_id = ? AND id = ?", orgID, id).
		First(&g).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m, err := loadSCIMGroupMembers(db, orgID, g.DisplayName)
	if err != nil {
		return nil, err
	}
	g.Members = m
	return &g, nil
}

func setGroupMembersTx(tx *gorm.DB, orgID, groupName string, memberIDs []string) error {
	if err := tx.Exec(`DELETE FROM private.user_groups WHERE org_id = ? AND name = ?`, orgID, groupName).Error; err != nil {
		return err
	}
	return addGroupMembersTx(tx, orgID, groupName, memberIDs)
}

func addGroupMembersTx(tx *gorm.DB, orgID, groupName string, memberIDs []string) error {
	seen := map[string]struct{}{}
	for _, uid := range memberIDs {
		if _, err := uuid.Parse(uid); err != nil {
			continue
		}
		if _, dup := seen[uid]; dup {
			continue
		}
		seen[uid] = struct{}{}
		// Only attach users that belong to this org.
		var exists bool
		if err := tx.Raw(`SELECT EXISTS(SELECT 1 FROM private.users WHERE org_id = ? AND id = ?)`, orgID, uid).Scan(&exists).Error; err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := tx.Exec(`
			INSERT INTO private.user_groups (org_id, user_id, name)
			VALUES (?, ?, ?)
			ON CONFLICT DO NOTHING`, orgID, uid, groupName).Error; err != nil {
			return err
		}
	}
	return nil
}

func CreateSCIMGroup(db *gorm.DB, orgID, displayName, externalID string, memberIDs []string) (*SCIMGroup, error) {
	displayName = strings.TrimSpace(displayName)
	var ext sql.NullString
	if externalID != "" {
		ext = sql.NullString{String: externalID, Valid: true}
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			INSERT INTO private.scim_groups (id, org_id, external_id, display_name, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			id, orgID, ext, displayName, now, now).Error; err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(err.Error(), "duplicate key") {
				return ErrAlreadyExists
			}
			return err
		}
		return setGroupMembersTx(tx, orgID, displayName, memberIDs)
	})
	if err != nil {
		return nil, err
	}
	return GetSCIMGroupByID(db, orgID, id)
}

func ReplaceSCIMGroup(db *gorm.DB, orgID, id, displayName string, externalID *string, memberIDs []string) (*SCIMGroup, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	displayName = strings.TrimSpace(displayName)
	err := db.Transaction(func(tx *gorm.DB) error {
		var cur SCIMGroup
		if err := tx.Table("private.scim_groups").
			Select("id, org_id, external_id, display_name").
			Where("org_id = ? AND id = ?", orgID, id).
			First(&cur).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		ext := cur.ExternalID
		if externalID != nil {
			if *externalID == "" {
				ext = sql.NullString{}
			} else {
				ext = sql.NullString{String: *externalID, Valid: true}
			}
		}
		if displayName == "" {
			displayName = cur.DisplayName
		}
		if err := tx.Exec(`
			UPDATE private.scim_groups SET display_name = ?, external_id = ?, updated_at = NOW()
			WHERE org_id = ? AND id = ?`,
			displayName, ext, orgID, id).Error; err != nil {
			return err
		}
		if cur.DisplayName != displayName {
			if err := tx.Exec(`DELETE FROM private.user_groups WHERE org_id = ? AND name = ?`,
				orgID, cur.DisplayName).Error; err != nil {
				return err
			}
		}
		return setGroupMembersTx(tx, orgID, displayName, memberIDs)
	})
	if err != nil {
		return nil, err
	}
	return GetSCIMGroupByID(db, orgID, id)
}

// PatchSCIMGroup updates a SCIM group's displayName and/or membership deltas
// inside one transaction.
func PatchSCIMGroup(db *gorm.DB, orgID, id string, newDisplayName *string, replaceMembers []string, addMembers, removeMembers []string, hasReplaceMembers bool) (*SCIMGroup, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		var cur SCIMGroup
		if err := tx.Table("private.scim_groups").
			Select("id, org_id, external_id, display_name").
			Where("org_id = ? AND id = ?", orgID, id).
			First(&cur).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		name := cur.DisplayName
		if newDisplayName != nil && strings.TrimSpace(*newDisplayName) != "" && strings.TrimSpace(*newDisplayName) != cur.DisplayName {
			name = strings.TrimSpace(*newDisplayName)
			if err := tx.Exec(`UPDATE private.scim_groups SET display_name = ?, updated_at = NOW() WHERE org_id = ? AND id = ?`,
				name, orgID, id).Error; err != nil {
				return err
			}
			if err := tx.Exec(`UPDATE private.user_groups SET name = ? WHERE org_id = ? AND name = ?`,
				name, orgID, cur.DisplayName).Error; err != nil {
				return err
			}
		}
		if hasReplaceMembers {
			if err := setGroupMembersTx(tx, orgID, name, replaceMembers); err != nil {
				return err
			}
		}
		if len(addMembers) > 0 {
			if err := addGroupMembersTx(tx, orgID, name, addMembers); err != nil {
				return err
			}
		}
		for _, uid := range removeMembers {
			if _, err := uuid.Parse(uid); err != nil {
				continue
			}
			if err := tx.Exec(`DELETE FROM private.user_groups WHERE org_id = ? AND name = ? AND user_id = ?`,
				orgID, name, uid).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return GetSCIMGroupByID(db, orgID, id)
}

func DeleteSCIMGroup(db *gorm.DB, orgID, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var cur SCIMGroup
		if err := tx.Table("private.scim_groups").
			Select("id, org_id, display_name").
			Where("org_id = ? AND id = ?", orgID, id).
			First(&cur).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if err := tx.Exec(`DELETE FROM private.user_groups WHERE org_id = ? AND name = ?`, orgID, cur.DisplayName).Error; err != nil {
			return err
		}
		return tx.Exec(`DELETE FROM private.scim_groups WHERE org_id = ? AND id = ?`, orgID, id).Error
	})
}
