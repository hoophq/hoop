package auditlog

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// List
//
//	@Summary		List security audit logs
//	@Description	Lists security audit log entries for the organization. Only admins can access this API. Supports filtering by actor, resource type, action, outcome, and date range. Results are ordered by created_at descending.
//	@Tags			Audit Logs
//	@Produce		json
//	@Param			page				query		int		false	"Page number (default: 1)"		default(1)
//	@Param			page_size			query		int		false	"Page size (1-100, default: 50)"	default(50)
//	@Param			actor_subject		query		string	false	"Filter by actor subject (partial match)"
//	@Param			actor_email		query		string	false	"Filter by actor email (partial match)"
//	@Param			resource_type		query		string	false	"Filter by resource type (e.g. connections, users, resources)"
//	@Param			action				query		string	false	"Filter by action (create, update, delete, revoke)"
//	@Param			resource_id		query		string	false	"Filter by resource ID (UUID)"
//	@Param			resource_name		query		string	false	"Filter by resource name (partial match)"
//	@Param			outcome				query		bool	false	"Filter by outcome (true = success, false = failure)"
//	@Param			created_after		query		string	false	"Filter entries created on or after this time (RFC3339 or YYYY-MM-DD)"
//	@Param			created_before		query		string	false	"Filter entries created on or before this time (RFC3339 or YYYY-MM-DD)"
//	@Success		200					{object}	openapi.PaginatedResponse[openapi.SecurityAuditLogResponse]
//	@Failure		400,403,500			{object}	openapi.HTTPError
//	@Router			/audit-logs [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	pageStr := c.Query("page")
	pageSizeStr := c.Query("page_size")
	page, pageSize, err := apivalidation.ParsePaginationParams(pageStr, pageSizeStr)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	f := models.SecurityAuditLogFilter{
		Page:         page,
		PageSize:     pageSize,
		ActorSubject: c.Query("actor_subject"),
		ActorEmail:   c.Query("actor_email"),
		ResourceType: c.Query("resource_type"),
		Action:       c.Query("action"),
		ResourceID:   c.Query("resource_id"),
		ResourceName: c.Query("resource_name"),
		CreatedAfter:  c.Query("created_after"),
		CreatedBefore: c.Query("created_before"),
	}

	if outcomeStr := c.Query("outcome"); outcomeStr != "" {
		outcome, err := strconv.ParseBool(outcomeStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"message": "outcome must be true or false"})
			return
		}
		f.Outcome = &outcome
	}

	rows, total, err := models.ListSecurityAuditLogs(models.DB, ctx.OrgID, f)
	if err != nil {
		log.Errorf("failed to list security audit logs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "internal server error"})
		return
	}

	data := make([]openapi.SecurityAuditLogResponse, len(rows))
	for i := range rows {
		data[i] = toResponse(&rows[i])
	}

	c.JSON(http.StatusOK, openapi.PaginatedResponse[openapi.SecurityAuditLogResponse]{
		Pages: openapi.Pagination{
			Total: int(total),
			Page:  f.Page,
			Size:  f.PageSize,
		},
		Data: data,
	})
}

func toResponse(r *models.SecurityAuditLog) openapi.SecurityAuditLogResponse {
	res := openapi.SecurityAuditLogResponse{
		ID:                     r.ID.String(),
		OrgID:                  r.OrgID,
		ActorSubject:           r.ActorSubject,
		ActorEmail:             r.ActorEmail,
		ActorName:              r.ActorName,
		CreatedAt:              r.CreatedAt,
		ResourceType:            r.ResourceType,
		Action:                 r.Action,
		ResourceName:           r.ResourceName,
		RequestPayloadRedacted: r.RequestPayloadRedacted,
		Outcome:                r.Outcome,
		ErrorMessage:           r.ErrorMessage,
	}
	if r.ResourceID != nil {
		res.ResourceID = r.ResourceID.String()
	}
	return res
}
