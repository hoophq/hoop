package apieventrouting

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/events"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/lib/pq"
)

func ListCatalog(c *gin.Context) {
	category := c.Query("category")
	var result []openapi.EventTypeResponse
	for _, et := range events.Catalog {
		if category != "" && !strings.EqualFold(et.Category, category) {
			continue
		}
		result = append(result, catalogEntryToResponse(et))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	c.JSON(http.StatusOK, result)
}

func GetCatalogEntry(c *gin.Context) {
	eventType := c.Param("event_type")
	et, ok := events.Catalog[eventType]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"message": "event type not found"})
		return
	}
	c.JSON(http.StatusOK, catalogEntryToResponse(et))
}

func ListSubscriptions(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	status := c.Query("status")

	subs, err := models.ListEventSubscriptions(ctx.GetOrgID(), status)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing event subscriptions: %v", err)
		return
	}

	result := make([]openapi.EventSubscriptionResponse, 0, len(subs))
	for _, s := range subs {
		result = append(result, subscriptionToResponse(s))
	}
	c.JSON(http.StatusOK, result)
}

func CreateSubscription(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	var req openapi.EventSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Errorf("failed parsing request payload, err=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	for _, et := range req.EventTypes {
		if _, ok := events.Catalog[et]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"message": "unknown event type: " + et})
			return
		}
	}

	if err := events.ValidateParameterMapping(req.EventTypes, req.ParameterMapping); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	conn, err := models.GetConnectionByNameOrID(ctx, req.ConnectionName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "connection not found: " + req.ConnectionName})
		return
	}

	status := "active"
	if req.Status == "paused" {
		status = "paused"
	}

	now := time.Now().UTC()
	sub := &models.EventSubscription{
		ID:                uuid.NewString(),
		OrgID:             ctx.GetOrgID(),
		Name:              req.Name,
		Description:       req.Description,
		EventTypes:        pq.StringArray(req.EventTypes),
		RunbookRepository: req.RunbookRepository,
		RunbookFile:       req.RunbookFile,
		ConnectionID:      conn.ID,
		ParameterMapping:  req.ParameterMapping,
		Status:            status,
		CreatedByUserID:   ctx.UserID,
		CreatedByEmail:    ctx.UserEmail,
		CreatedByGroups:   pq.StringArray(ctx.UserGroups),
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	if err := models.CreateEventSubscription(sub); err != nil {
		if err == models.ErrAlreadyExists {
			c.JSON(http.StatusConflict, gin.H{"message": "subscription with this name already exists"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating event subscription: %v", err)
		return
	}

	resp := subscriptionToResponse(sub)
	resp.ConnectionName = conn.Name
	c.JSON(http.StatusCreated, resp)
}

func GetSubscription(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	id := c.Param("id")

	sub, err := models.GetEventSubscriptionByID(ctx.GetOrgID(), id)
	if err != nil {
		if err == models.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed getting event subscription: %v", err)
		return
	}

	resp := subscriptionToResponse(sub)

	delivered, failed, lastErr, err := models.GetDispatchStats(ctx.GetOrgID(), id, 7)
	if err != nil {
		log.Errorf("failed getting dispatch stats for subscription %s: %v", id, err)
	} else {
		resp.DeliveredCount7d = delivered
		resp.FailedCount7d = failed
		resp.LastError = lastErr
	}

	c.JSON(http.StatusOK, resp)
}

func UpdateSubscription(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	id := c.Param("id")

	var req openapi.EventSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Errorf("failed parsing request payload, err=%v", err)
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	for _, et := range req.EventTypes {
		if _, ok := events.Catalog[et]; !ok {
			c.JSON(http.StatusBadRequest, gin.H{"message": "unknown event type: " + et})
			return
		}
	}

	if err := events.ValidateParameterMapping(req.EventTypes, req.ParameterMapping); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	conn, err := models.GetConnectionByNameOrID(ctx, req.ConnectionName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "connection not found: " + req.ConnectionName})
		return
	}

	sub := &models.EventSubscription{
		ID:                id,
		OrgID:             ctx.GetOrgID(),
		Name:              req.Name,
		Description:       req.Description,
		EventTypes:        pq.StringArray(req.EventTypes),
		RunbookRepository: req.RunbookRepository,
		RunbookFile:       req.RunbookFile,
		ConnectionID:      conn.ID,
		ParameterMapping:  req.ParameterMapping,
		UpdatedAt:         time.Now().UTC(),
	}

	if err := models.UpdateEventSubscription(sub); err != nil {
		if err == models.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating event subscription: %v", err)
		return
	}

	updated, err := models.GetEventSubscriptionByID(ctx.GetOrgID(), id)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching updated subscription: %v", err)
		return
	}

	resp := subscriptionToResponse(updated)
	resp.ConnectionName = conn.Name
	c.JSON(http.StatusOK, resp)
}

func DeleteSubscription(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	id := c.Param("id")

	err := models.DeleteEventSubscription(ctx.GetOrgID(), id)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.Writer.WriteHeader(http.StatusNoContent)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting event subscription: %v", err)
	}
}

func PauseSubscription(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	id := c.Param("id")

	err := models.SetEventSubscriptionStatus(ctx.GetOrgID(), id, "paused")
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.JSON(http.StatusOK, gin.H{"status": "paused"})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed pausing event subscription: %v", err)
	}
}

func ResumeSubscription(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	id := c.Param("id")

	err := models.SetEventSubscriptionStatus(ctx.GetOrgID(), id, "active")
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
	case nil:
		c.JSON(http.StatusOK, gin.H{"status": "active"})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed resuming event subscription: %v", err)
	}
}

func ListDispatches(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	subID := c.Param("id")
	page := queryInt(c, "page", 1)
	pageSize := queryInt(c, "page_size", 50)
	if pageSize > 100 {
		pageSize = 100
	}
	offset := (page - 1) * pageSize

	items, total, err := models.ListDispatchesForSubscription(ctx.GetOrgID(), subID, pageSize, offset)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing dispatches: %v", err)
		return
	}

	respItems := make([]openapi.EventDispatchListItemResponse, 0, len(items))
	for _, item := range items {
		respItems = append(respItems, dispatchListItemToResponse(item))
	}

	c.JSON(http.StatusOK, openapi.EventDispatchListResponse{
		Items: respItems,
		Total: total,
		Page:  page,
		Limit: pageSize,
	})
}

func GetDispatch(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	id := c.Param("id")

	dispatch, err := models.GetDispatchByID(ctx.GetOrgID(), id)
	if err != nil {
		if err == models.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed getting dispatch: %v", err)
		return
	}

	sub, event, err := models.GetDispatchContext(models.DB, id)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed getting dispatch context: %v", err)
		return
	}

	listItem := &models.EventDispatchListItem{
		ID:           dispatch.ID,
		EventID:      dispatch.EventID,
		EventType:    event.EventType,
		Status:       dispatch.Status,
		Attempt:      dispatch.Attempt,
		SessionID:    dispatch.SessionID,
		LastError:    dispatch.LastError,
		ReplayedFrom: dispatch.ReplayedFrom,
		OccurredAt:   event.OccurredAt,
		CreatedAt:    dispatch.CreatedAt,
		DispatchedAt: dispatch.DispatchedAt,
		FinishedAt:   dispatch.FinishedAt,
	}

	resp := openapi.EventDispatchDetailResponse{
		EventDispatchListItemResponse: dispatchListItemToResponse(listItem),
		EventPayload:                  event.Payload,
		SubscriptionName:              sub.Name,
	}
	c.JSON(http.StatusOK, resp)
}

func ReplayDispatch(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	id := c.Param("id")

	original, err := models.GetDispatchByID(ctx.GetOrgID(), id)
	if err != nil {
		if err == models.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": "resource not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed getting dispatch: %v", err)
		return
	}

	if original.Status == "pending" || original.Status == "processing" {
		c.JSON(http.StatusConflict, gin.H{"message": "cannot replay a dispatch that is " + original.Status})
		return
	}

	replay, err := models.CreateReplayDispatch(ctx.GetOrgID(), original)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating replay dispatch: %v", err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":            replay.ID,
		"status":        replay.Status,
		"replayed_from": original.ID,
	})
}

func catalogEntryToResponse(et events.EventType) openapi.EventTypeResponse {
	schema := make([]openapi.EventSchemaFieldResponse, 0, len(et.Schema))
	for _, f := range et.Schema {
		schema = append(schema, openapi.EventSchemaFieldResponse{
			Name:     f.Name,
			Type:     f.Type,
			Required: f.Required,
		})
	}
	return openapi.EventTypeResponse{
		Name:          et.Name,
		Category:      et.Category,
		Description:   et.Description,
		Schema:        schema,
		SamplePayload: et.SamplePayload,
	}
}

func subscriptionToResponse(s *models.EventSubscription) openapi.EventSubscriptionResponse {
	return openapi.EventSubscriptionResponse{
		ID:                s.ID,
		Name:              s.Name,
		Description:       s.Description,
		EventTypes:        []string(s.EventTypes),
		RunbookRepository: s.RunbookRepository,
		RunbookFile:       s.RunbookFile,
		ConnectionID:      s.ConnectionID,
		ParameterMapping:  s.ParameterMapping,
		Status:            s.Status,
		CreatedByEmail:    s.CreatedByEmail,
		CreatedAt:         s.CreatedAt,
		UpdatedAt:         s.UpdatedAt,
	}
}

func dispatchListItemToResponse(item *models.EventDispatchListItem) openapi.EventDispatchListItemResponse {
	resp := openapi.EventDispatchListItemResponse{
		ID:           item.ID,
		EventID:      item.EventID,
		EventType:    item.EventType,
		Status:       item.Status,
		Attempt:      item.Attempt,
		OccurredAt:   item.OccurredAt,
		CreatedAt:    item.CreatedAt,
		DispatchedAt: item.DispatchedAt,
		FinishedAt:   item.FinishedAt,
	}
	if item.SessionID.Valid {
		resp.SessionID = &item.SessionID.String
	}
	if item.LastError.Valid {
		resp.LastError = &item.LastError.String
	}
	if item.ReplayedFrom.Valid {
		resp.ReplayedFrom = &item.ReplayedFrom.String
	}
	if item.DispatchedAt != nil && item.FinishedAt != nil {
		ms := item.FinishedAt.Sub(*item.DispatchedAt).Milliseconds()
		resp.DurationMS = &ms
	}
	return resp
}

func queryInt(c *gin.Context, key string, defaultVal int) int {
	s := c.Query(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return defaultVal
	}
	return v
}
