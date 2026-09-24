package apiscim

import (
	"net/http"
	"strings"

	"github.com/elimity-com/scim"
	"github.com/elimity-com/scim/optional"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	filter "github.com/scim2/filter-parser/v2"
	"gorm.io/gorm"
)

// groupHandler maps SCIM groups onto hoop groups. The display name is the
// group name an approval rule names in reviewers_groups; members are hoop user
// ids, the SCIM ids userHandler hands out.
type groupHandler struct{}

func loadGroup(db *gorm.DB, orgID, id string) (*models.DirectoryGroup, error) {
	// The column is a uuid: anything else is simply not a group of ours.
	if _, err := uuid.Parse(id); err != nil {
		return nil, gorm.ErrRecordNotFound
	}
	return models.GetDirectoryGroup(db, orgID, id)
}

func groupResource(db *gorm.DB, orgID string, g *models.DirectoryGroup) (scim.Resource, error) {
	ids, err := models.ListGroupMemberIDs(db, orgID, g.DisplayName)
	if err != nil {
		return scim.Resource{}, err
	}
	emails := map[string]string{}
	if len(ids) > 0 {
		var rows []struct{ ID, Email string }
		if err := db.Raw(`SELECT id::TEXT AS id, email FROM private.users WHERE org_id = ? AND id::TEXT IN ?`,
			orgID, ids).Scan(&rows).Error; err != nil {
			return scim.Resource{}, err
		}
		for _, r := range rows {
			emails[r.ID] = r.Email
		}
	}
	members := make([]any, 0, len(ids))
	for _, id := range ids {
		members = append(members, map[string]any{"value": id, "display": emails[id]})
	}
	created, updated := g.CreatedAt, g.UpdatedAt
	res := scim.Resource{
		ID:         g.ID,
		Attributes: scim.ResourceAttributes{"displayName": g.DisplayName, "members": members},
		Meta:       scim.Meta{Created: &created, LastModified: &updated},
	}
	if g.ExternalID != nil && *g.ExternalID != "" {
		res.ExternalID = optional.NewString(*g.ExternalID)
	}
	return res, nil
}

func getGroup(orgID, id string) (scim.Resource, error) {
	g, err := loadGroup(models.DB, orgID, id)
	if err != nil {
		return scim.Resource{}, toSCIMError(id, err)
	}
	res, err := groupResource(models.DB, orgID, g)
	if err != nil {
		return scim.Resource{}, toSCIMError(id, err)
	}
	return res, nil
}

func (groupHandler) Create(r *http.Request, attrs scim.ResourceAttributes) (scim.Resource, error) {
	orgID := orgFromRequest(r)
	var id string
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		g, err := services.CreateProvisionedGroup(tx, orgID, models.ProvisioningSourceSCIM,
			stringAttr(attrs, "displayName"), externalID(attrs))
		if err != nil {
			return err
		}
		id = g.ID
		return services.SetGroupMembers(tx, orgID, g.DisplayName, memberIDs(listAttr(attrs, "members")))
	})
	if err != nil {
		return scim.Resource{}, toSCIMError("", err)
	}
	return getGroup(orgID, id)
}

func (groupHandler) Get(r *http.Request, id string) (scim.Resource, error) {
	return getGroup(orgFromRequest(r), id)
}

func (groupHandler) GetAll(r *http.Request, params scim.ListRequestParams) (scim.Page, error) {
	orgID := orgFromRequest(r)
	groups, err := models.ListDirectoryGroups(models.DB, orgID, models.ProvisioningSourceSCIM)
	if err != nil {
		return scim.Page{}, toSCIMError("", err)
	}
	resources := make([]scim.Resource, 0, len(groups))
	for i := range groups {
		res, err := groupResource(models.DB, orgID, &groups[i])
		if err != nil {
			return scim.Page{}, toSCIMError(groups[i].ID, err)
		}
		resources = append(resources, res)
	}
	return page(resources, params)
}

func (groupHandler) Replace(r *http.Request, id string, attrs scim.ResourceAttributes) (scim.Resource, error) {
	orgID := orgFromRequest(r)
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		g, err := loadGroup(tx, orgID, id)
		if err != nil {
			return err
		}
		if name := stringAttr(attrs, "displayName"); name != "" {
			if err := services.RenameProvisionedGroup(tx, orgID, g, name); err != nil {
				return err
			}
		}
		// A replace without members leaves them alone rather than emptying
		// the group: clients that rename send only the name.
		if _, ok := attr(attrs, "members"); !ok {
			return nil
		}
		return services.SetGroupMembers(tx, orgID, g.DisplayName, memberIDs(listAttr(attrs, "members")))
	})
	if err != nil {
		return scim.Resource{}, toSCIMError(id, err)
	}
	return getGroup(orgID, id)
}

func (groupHandler) Patch(r *http.Request, id string, ops []scim.PatchOperation) (scim.Resource, error) {
	orgID := orgFromRequest(r)
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		g, err := loadGroup(tx, orgID, id)
		if err != nil {
			return err
		}
		for _, op := range ops {
			if err := applyGroupPatch(tx, orgID, g, op); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return scim.Resource{}, toSCIMError(id, err)
	}
	return getGroup(orgID, id)
}

// applyGroupPatch applies one operation. Okta renames with no path and a value
// object; both Okta and Entra ID add and remove members by path, one member at
// a time with a value filter or several in a value list.
func applyGroupPatch(tx *gorm.DB, orgID string, g *models.DirectoryGroup, op scim.PatchOperation) error {
	if op.Path == nil {
		values, _ := op.Value.(map[string]any)
		if name := stringAttr(values, "displayName"); name != "" && op.Op != scim.PatchOperationRemove {
			if err := services.RenameProvisionedGroup(tx, orgID, g, name); err != nil {
				return err
			}
		}
		if members, ok := attr(values, "members"); ok {
			return patchMembers(tx, orgID, g.DisplayName, op.Op, nil, members)
		}
		return nil
	}

	switch strings.ToLower(op.Path.AttributePath.AttributeName) {
	case "displayname":
		name, _ := op.Value.(string)
		if op.Op == scim.PatchOperationRemove || strings.TrimSpace(name) == "" {
			return nil
		}
		return services.RenameProvisionedGroup(tx, orgID, g, name)
	case "members":
		return patchMembers(tx, orgID, g.DisplayName, op.Op, op.Path.ValueExpression, op.Value)
	}
	return nil
}

func patchMembers(tx *gorm.DB, orgID, name, op string, selector filter.Expression, value any) error {
	ids := memberIDs(listValue(value))
	switch op {
	case scim.PatchOperationAdd:
		return services.AddGroupMembers(tx, orgID, name, ids)
	case scim.PatchOperationReplace:
		return services.SetGroupMembers(tx, orgID, name, ids)
	case scim.PatchOperationRemove:
		if id := selectedMember(selector); id != "" {
			return services.RemoveGroupMembers(tx, orgID, name, []string{id})
		}
		if len(ids) > 0 {
			return services.RemoveGroupMembers(tx, orgID, name, ids)
		}
		return services.RemoveAllGroupMembers(tx, orgID, name)
	}
	return nil
}

// selectedMember reads members[value eq "<id>"], the one filter clients use to
// name a member.
func selectedMember(selector filter.Expression) string {
	expr, ok := selector.(*filter.AttributeExpression)
	if !ok || expr.Operator != filter.EQ || !strings.EqualFold(expr.AttributePath.AttributeName, "value") {
		return ""
	}
	id, _ := expr.CompareValue.(string)
	return id
}

func (groupHandler) Delete(r *http.Request, id string) error {
	orgID := orgFromRequest(r)
	err := models.DB.Transaction(func(tx *gorm.DB) error {
		g, err := loadGroup(tx, orgID, id)
		if err != nil {
			return err
		}
		return services.DeleteProvisionedGroup(tx, orgID, g)
	})
	if err != nil {
		return toSCIMError(id, err)
	}
	return nil
}
