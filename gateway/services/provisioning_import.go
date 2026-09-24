package services

import (
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"gorm.io/gorm"
)

// ErrGroupsManaged refuses a file import while the Slack import owns the
// org's groups: its next run would undo it.
var ErrGroupsManaged = errors.New("groups are managed by the Slack import; stop managing them before importing a file")

// ImportRow is one user of a file import. Row is its 1-based position, for
// error reports.
type ImportRow struct {
	Row    int
	Email  string
	Name   string
	Groups []string
}

// ImportRowError is why one row was not imported.
type ImportRowError struct {
	Row     int
	Email   string
	Message string
}

// ImportResult is what a file import did.
type ImportResult struct {
	Created     int
	Updated     int
	Deactivated int
	Errors      []ImportRowError
}

// ImportUsers writes the users of a file and their groups, through the same
// seam the Slack import uses, with source "file".
//
// Each row is written on its own, so a bad row fails that row and not the
// file. Every group the file names then gets exactly the imported users who
// list it as members; groups it does not name are left alone, and so is the
// admin group, which a file cannot name.
//
// deactivateMissing deactivates the users an earlier file import created who
// are not in this one, except administrators.
func ImportUsers(db *gorm.DB, orgID string, rows []ImportRow, deactivateMissing bool) (*ImportResult, error) {
	managed, err := models.GroupsManagedByProvisioning(db, orgID)
	if err != nil {
		return nil, err
	}
	if managed {
		return nil, ErrGroupsManaged
	}

	res := &ImportResult{Errors: []ImportRowError{}}
	imported := map[string]bool{}    // hoop user id
	members := map[string][]string{} // group -> hoop user ids
	var groupOrder []string

	for _, row := range rows {
		email := normalizeEmail(row.Email)
		fail := func(msg string) {
			res.Errors = append(res.Errors, ImportRowError{Row: row.Row, Email: email, Message: msg})
		}
		if _, err := mail.ParseAddress(email); err != nil || !strings.Contains(email, "@") {
			fail("the email is not a valid address")
			continue
		}
		groups, err := importGroups(row.Groups)
		if err != nil {
			fail(err.Error())
			continue
		}

		var upserted UpsertResult
		err = db.Transaction(func(tx *gorm.DB) error {
			var err error
			upserted, err = UpsertProvisionedUserResult(tx, orgID, models.ProvisioningSourceFile, "", ProvisionedUser{
				ExternalID: email,
				UserName:   email,
				Email:      email,
				Name:       row.Name,
				Active:     true,
			})
			return err
		})
		if err != nil {
			fail(err.Error())
			continue
		}
		if upserted.Created {
			res.Created++
		} else if !imported[upserted.UserID] {
			res.Updated++
		}
		imported[upserted.UserID] = true
		for _, g := range groups {
			if _, seen := members[g]; !seen {
				groupOrder = append(groupOrder, g)
			}
			if !slices.Contains(members[g], upserted.UserID) {
				members[g] = append(members[g], upserted.UserID)
			}
		}
	}

	err = db.Transaction(func(tx *gorm.DB) error {
		for _, g := range groupOrder {
			group, err := EnsureProvisionedGroup(tx, orgID, models.ProvisioningSourceFile, g, g)
			if err != nil {
				return fmt.Errorf("group %s: %w", g, err)
			}
			if err := SetGroupMembers(tx, orgID, group.DisplayName, members[g]); err != nil {
				return err
			}
		}
		if !deactivateMissing {
			return nil
		}
		links, err := models.ListDirectoryUsers(tx, orgID, models.ProvisioningSourceFile)
		if err != nil {
			return err
		}
		for _, link := range links {
			if imported[link.UserID] {
				continue
			}
			var admin bool
			if err := tx.Raw(`SELECT EXISTS (
				SELECT 1 FROM private.user_groups WHERE org_id = ? AND user_id = ? AND name = ?)`,
				orgID, link.UserID, types.GroupAdmin).Scan(&admin).Error; err != nil {
				return err
			}
			if admin {
				continue
			}
			var status string
			if err := tx.Raw(`SELECT status FROM private.users WHERE id = ?`, link.UserID).Scan(&status).Error; err != nil {
				return err
			}
			if status == "inactive" {
				continue
			}
			if err := DeactivateProvisionedUser(tx, orgID, link.UserID); err != nil {
				return err
			}
			res.Deactivated++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// importGroups trims, validates and dedupes a row's groups.
func importGroups(raw []string) ([]string, error) {
	var out []string
	for _, g := range raw {
		if strings.TrimSpace(g) == "" {
			continue
		}
		name, err := ValidateProvisionedGroupName(g)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out, nil
}
