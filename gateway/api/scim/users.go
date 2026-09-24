package apiscim

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/elimity-com/scim"
	"github.com/elimity-com/scim/optional"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"gorm.io/gorm"
)

// userHandler maps SCIM users onto hoop users. The SCIM id is the hoop user
// id, which is also what a group's members carry.
//
// It lists every user of the organization, not only the ones SCIM created: an
// identity provider looks a user up by userName before creating it, and
// finding one that already logged in lets it take that account over instead
// of creating a second one with the same email.
type userHandler struct{}

type userRow struct {
	ID         string
	Email      string
	Name       string
	Status     string
	UserName   sql.NullString
	ExternalID sql.NullString
}

func loadUsers(db *gorm.DB, orgID, userID string) ([]userRow, error) {
	q := `
		SELECT u.id::TEXT AS id, u.email, u.name, u.status::TEXT AS status, d.user_name, d.external_id
		FROM private.users u
		LEFT JOIN private.directory_users d ON d.user_id = u.id AND d.source = @source
		WHERE u.org_id = @org`
	args := map[string]any{"org": orgID, "source": models.ProvisioningSourceSCIM}
	if userID != "" {
		q += ` AND u.id::TEXT = @id`
		args["id"] = userID
	}
	var rows []userRow
	err := db.Raw(q+` ORDER BY u.email, u.id`, args).Scan(&rows).Error
	return rows, err
}

func loadUser(db *gorm.DB, orgID, userID string) (*userRow, error) {
	rows, err := loadUsers(db, orgID, userID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &rows[0], nil
}

func (row userRow) userName() string {
	if row.UserName.Valid && row.UserName.String != "" {
		return row.UserName.String
	}
	return row.Email
}

func (row userRow) resource() scim.Resource {
	res := scim.Resource{
		ID: row.ID,
		Attributes: scim.ResourceAttributes{
			"userName":    row.userName(),
			"displayName": row.Name,
			"name":        map[string]any{"formatted": row.Name},
			"emails":      []any{map[string]any{"value": row.Email, "primary": true, "type": "work"}},
			"active":      row.Status == "active",
		},
	}
	if row.ExternalID.Valid && row.ExternalID.String != "" {
		res.ExternalID = optional.NewString(row.ExternalID.String)
	}
	return res
}

// provisioned is the user as hoop stores it, the base a replace or a patch
// starts from.
func (row userRow) provisioned() services.ProvisionedUser {
	return services.ProvisionedUser{
		ExternalID: row.ExternalID.String,
		UserName:   row.userName(),
		Email:      row.Email,
		Name:       row.Name,
		Active:     row.Status == "active",
	}
}

// applyUserAttributes overwrites what the attributes carry and keeps the rest.
func applyUserAttributes(u *services.ProvisionedUser, attrs map[string]any) {
	if v := stringAttr(attrs, "userName"); v != "" {
		u.UserName = v
	}
	if v := externalID(attrs); v != "" {
		u.ExternalID = v
	}
	if v := primaryEmail(listAttr(attrs, "emails")); v != "" {
		u.Email = v
	}
	if v := displayName(attrs); v != "" {
		u.Name = v
	}
	if v, ok := attr(attrs, "active"); ok {
		u.Active = boolValue(v, u.Active)
	}
}

func (userHandler) Create(r *http.Request, attrs scim.ResourceAttributes) (scim.Resource, error) {
	orgID := orgFromRequest(r)
	u := services.ProvisionedUser{Active: true}
	applyUserAttributes(&u, attrs)

	var id string
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		taken, err := services.ProvisionedUserNameTaken(tx, orgID, models.ProvisioningSourceSCIM, u.UserName)
		if err != nil {
			return err
		}
		if taken {
			return services.ErrProvisionedUserExists
		}
		id, err = services.UpsertProvisionedUser(tx, orgID, models.ProvisioningSourceSCIM, "", u)
		return err
	})
	if err != nil {
		return scim.Resource{}, toSCIMError("", err)
	}
	return getUser(orgID, id)
}

func (userHandler) Get(r *http.Request, id string) (scim.Resource, error) {
	return getUser(orgFromRequest(r), id)
}

func getUser(orgID, id string) (scim.Resource, error) {
	row, err := loadUser(models.DB, orgID, id)
	if err != nil {
		return scim.Resource{}, toSCIMError(id, err)
	}
	return row.resource(), nil
}

func (userHandler) GetAll(r *http.Request, params scim.ListRequestParams) (scim.Page, error) {
	rows, err := loadUsers(models.DB, orgFromRequest(r), "")
	if err != nil {
		return scim.Page{}, toSCIMError("", err)
	}
	resources := make([]scim.Resource, 0, len(rows))
	for _, row := range rows {
		resources = append(resources, row.resource())
	}
	return page(resources, params)
}

func (userHandler) Replace(r *http.Request, id string, attrs scim.ResourceAttributes) (scim.Resource, error) {
	return updateUser(orgFromRequest(r), id, func(u *services.ProvisionedUser) {
		applyUserAttributes(u, attrs)
	})
}

func (userHandler) Patch(r *http.Request, id string, ops []scim.PatchOperation) (scim.Resource, error) {
	return updateUser(orgFromRequest(r), id, func(u *services.ProvisionedUser) {
		applyUserPatch(u, ops)
	})
}

func updateUser(orgID, id string, change func(u *services.ProvisionedUser)) (scim.Resource, error) {
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		row, err := loadUser(tx, orgID, id)
		if err != nil {
			return err
		}
		u := row.provisioned()
		change(&u)
		_, err = services.UpsertProvisionedUser(tx, orgID, models.ProvisioningSourceSCIM, id, u)
		return err
	})
	if err != nil {
		return scim.Resource{}, toSCIMError(id, err)
	}
	return getUser(orgID, id)
}

// applyUserPatch applies the operations identity providers send for a user.
// Okta replaces with no path and a value object; Entra ID addresses each
// attribute by path, emails included. Removing one of these attributes is
// ignored: hoop keeps a user's email and name, and deactivation is "active".
func applyUserPatch(u *services.ProvisionedUser, ops []scim.PatchOperation) {
	var given, family string
	for _, op := range ops {
		if op.Op == scim.PatchOperationRemove {
			continue
		}
		if op.Path == nil {
			values, _ := op.Value.(map[string]any)
			applyUserAttributes(u, values)
			for k, v := range values {
				s, _ := v.(string)
				switch strings.ToLower(k) {
				case "name.givenname":
					given = s
				case "name.familyname":
					family = s
				case "name.formatted":
					u.Name = strings.TrimSpace(s)
				case `emails[type eq "work"].value`:
					u.Email = strings.TrimSpace(s)
				}
			}
			continue
		}

		sub := strings.ToLower(op.Path.AttributePath.SubAttributeName())
		if op.Path.SubAttribute != nil {
			sub = strings.ToLower(*op.Path.SubAttribute)
		}
		s, _ := op.Value.(string)
		s = strings.TrimSpace(s)
		switch strings.ToLower(op.Path.AttributePath.AttributeName) {
		case "active":
			u.Active = boolValue(op.Value, u.Active)
		case "username":
			if s != "" {
				u.UserName = s
			}
		case "displayname":
			if s != "" {
				u.Name = s
			}
		case "externalid":
			if s != "" {
				u.ExternalID = s
			}
		case "emails":
			if s != "" {
				u.Email = s
			} else if e := primaryEmail(listValue(op.Value)); e != "" {
				u.Email = e
			}
		case "name":
			switch sub {
			case "givenname":
				given = s
			case "familyname":
				family = s
			case "formatted":
				if s != "" {
					u.Name = s
				}
			case "":
				if m, ok := op.Value.(map[string]any); ok {
					if n := displayName(map[string]any{"name": m}); n != "" {
						u.Name = n
					}
				}
			}
		}
	}
	if n := strings.TrimSpace(given + " " + family); n != "" && (given != "" || family != "") {
		u.Name = n
	}
}

func (userHandler) Delete(r *http.Request, id string) error {
	orgID := orgFromRequest(r)
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		// Looked up first: the id comes from the URL, and one that is not a
		// uuid must answer 404 rather than fail the uuid cast.
		if _, err := loadUser(tx, orgID, id); err != nil {
			return err
		}
		return services.DeactivateProvisionedUser(tx, orgID, id)
	})
	if err != nil {
		return toSCIMError(id, err)
	}
	return nil
}
