package reviewapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services/approvalrouting"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

// toOpenApiRouting renders a stored routing decision. User names and
// emails are filled by resolveRoutingUsers: the decision stores ids only.
func toOpenApiRouting(r *models.ReviewRouting) *openapi.ReviewRouting {
	if r == nil {
		return nil
	}
	out := &openapi.ReviewRouting{
		Mode:           r.Mode,
		FallbackCause:  r.FallbackCause,
		FallbackDetail: r.FallbackDetail,
		Reason:         r.Reason,
		Candidates:     append([]string{}, r.Candidates...),
		Groups:         append([]string{}, r.Groups...),
		NotifyFallback:       r.NotifyFallback,
		NotifyFallbackPosted: r.NotifyFallbackPosted,
		DecidedAt:      r.DecidedAt,
	}
	for _, i := range r.Integrations {
		out.Integrations = append(out.Integrations, openapi.ReviewRoutingIntegration{
			Name: i.Name, Delivery: i.Delivery, Integrated: i.Integrated})
	}
	for _, u := range r.NotifiedUsers {
		out.NotifiedUsers = append(out.NotifiedUsers, openapi.ReviewRoutingUser{
			ID: u.ID, Integrations: append([]string{}, u.Integrations...)})
	}
	for _, c := range r.Channels {
		name := c.Name
		if name == "" {
			name = c.ID
		}
		out.Channels = append(out.Channels, openapi.ReviewRoutingChannel{
			Integration: c.Integration, ID: c.ID, Name: name})
	}
	for _, u := range r.Unreachable {
		out.Unreachable = append(out.Unreachable, openapi.ReviewRoutingUnreachable{
			UserID: u.UserID, Integration: u.Integration, Cause: u.Cause})
	}
	for _, c := range r.UndeliveredChannels {
		out.UndeliveredChannels = append(out.UndeliveredChannels, openapi.ReviewRoutingUndeliveredChannel{
			Integration: c.Integration, ID: c.ID, Cause: c.Cause})
	}
	return out
}

// resolveRoutingUsers fills the names and emails of the routing users of
// reviews, with one query. Only the API response carries them; the routing
// prompt never does. A failed read leaves the ids alone.
func resolveRoutingUsers(db *gorm.DB, orgID string, reviews []*openapi.Review) {
	var ids []string
	for _, r := range reviews {
		if r == nil || r.Routing == nil {
			continue
		}
		for _, u := range r.Routing.NotifiedUsers {
			ids = append(ids, u.ID)
		}
		for _, u := range r.Routing.Unreachable {
			ids = append(ids, u.UserID)
		}
	}
	if len(ids) == 0 {
		return
	}
	var users []struct {
		ID    string `gorm:"column:id"`
		Name  string `gorm:"column:name"`
		Email string `gorm:"column:email"`
	}
	err := db.Raw(`SELECT id, name, email FROM private.users WHERE org_id = ? AND id IN ?`, orgID, ids).
		Scan(&users).Error
	if err != nil {
		log.With("org", orgID).Warnf("failed reading the routing users of the approval requests, reason=%v", err)
		return
	}
	byID := make(map[string]int, len(users))
	for i, u := range users {
		byID[u.ID] = i
	}
	for _, r := range reviews {
		if r == nil || r.Routing == nil {
			continue
		}
		for i, u := range r.Routing.NotifiedUsers {
			if j, ok := byID[u.ID]; ok {
				r.Routing.NotifiedUsers[i].Name, r.Routing.NotifiedUsers[i].Email = users[j].Name, users[j].Email
			}
		}
		for i, u := range r.Routing.Unreachable {
			if j, ok := byID[u.UserID]; ok {
				r.Routing.Unreachable[i].Name = users[j].Name
			}
		}
	}
}

// GetRoutingIntegrations
//
//	@Summary		Approval Routing Integrations
//	@Description	List the integrations AI routing can notify through, and the channels each reaches. Slack channels are the channels of every sidecar listener and the default channel; a channel name is its id when unknown. An integration that is not set up reads integrated false.
//	@Tags			Approvals
//	@Produce		json
//	@Success		200		{object}	openapi.ApprovalRoutingIntegrations
//	@Failure		401,500	{object}	openapi.HTTPError
//	@Router			/approvals/routing/integrations [get]
func GetRoutingIntegrations(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	states, err := approvalrouting.ListIntegrations(c.Request.Context(), models.DB, ctx.GetOrgID())
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing the routing integrations")
		return
	}
	render := func(s approvalrouting.IntegrationState) openapi.ApprovalRoutingIntegration {
		out := openapi.ApprovalRoutingIntegration{Integrated: s.Integrated, Channels: []openapi.ApprovalRoutingChannel{}}
		for _, ch := range s.Channels {
			name := ch.Name
			if name == "" {
				name = ch.ID
			}
			out.Channels = append(out.Channels, openapi.ApprovalRoutingChannel{ID: ch.ID, Name: name})
		}
		return out
	}
	c.JSON(http.StatusOK, openapi.ApprovalRoutingIntegrations{
		Slack: render(states[approvalrouting.IntegrationSlack]),
		Teams: render(states[approvalrouting.IntegrationTeams]),
	})
}
