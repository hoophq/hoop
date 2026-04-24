// Package spiffemappings implements the HTTP admin API for managing
// SPIFFE-ID to Hoop-agent mappings. These mappings are consumed at
// authentication time when an agent presents a SPIFFE JWT-SVID.
package spiffemappings

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/audit"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// List
//
//	@Summary		List SPIFFE Mappings
//	@Description	List all SPIFFE-ID to agent mappings in the caller's organization.
//	@Tags			SPIFFE
//	@Produce		json
//	@Success		200	{array}		openapi.AgentSPIFFEMapping
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/spiffe-mappings [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	items, err := models.ListAgentSPIFFEMappings(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing spiffe mappings: %v", err)
		return
	}
	out := make([]openapi.AgentSPIFFEMapping, 0, len(items))
	for _, m := range items {
		out = append(out, toOpenAPI(m))
	}
	c.JSON(http.StatusOK, out)
}

// Create
//
//	@Summary		Create SPIFFE Mapping
//	@Description	Map a SPIFFE identity (exact URI or URI prefix) onto a Hoop agent.
//	@Tags			SPIFFE
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.AgentSPIFFEMapping	true	"The mapping to create"
//	@Success		201				{object}	openapi.AgentSPIFFEMapping
//	@Failure		400,409,422,500	{object}	openapi.HTTPError
//	@Router			/spiffe-mappings [post]
func Create(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.AgentSPIFFEMapping
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	m := toModel(&req, ctx.OrgID)
	m.ID = "" // let DB generate
	evt := audit.NewEvent(audit.ResourceAgentSPIFFEMapping, audit.ActionCreate).
		Set("trust_domain", req.TrustDomain).
		Set("spiffe_id", req.SPIFFEID).
		Set("spiffe_prefix", req.SPIFFEPrefix).
		Set("agent_id", req.AgentID).
		Set("agent_template", req.AgentTemplate).
		Set("groups", req.Groups)
	defer func() { evt.Log(c) }()

	err := models.CreateAgentSPIFFEMapping(m)
	evt.Err(err)
	switch {
	case errors.Is(err, models.ErrAlreadyExists):
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
	case err != nil && isValidationErr(err):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating spiffe mapping: %v", err)
	default:
		out := toOpenAPI(*m)
		evt.Resource(out.ID, labelFor(out))
		c.JSON(http.StatusCreated, out)
	}
}

// Update
//
//	@Summary		Update SPIFFE Mapping
//	@Description	Update an existing SPIFFE-ID to agent mapping.
//	@Tags			SPIFFE
//	@Accept			json
//	@Produce		json
//	@Param			id				path		string						true	"Mapping ID"
//	@Param			request			body		openapi.AgentSPIFFEMapping	true	"The mapping fields to update"
//	@Success		200				{object}	openapi.AgentSPIFFEMapping
//	@Failure		400,404,422,500	{object}	openapi.HTTPError
//	@Router			/spiffe-mappings/{id} [put]
func Update(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.AgentSPIFFEMapping
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	m := toModel(&req, ctx.OrgID)
	m.ID = c.Param("id")
	evt := audit.NewEvent(audit.ResourceAgentSPIFFEMapping, audit.ActionUpdate).
		Resource(m.ID, labelFor(req)).
		Set("trust_domain", req.TrustDomain).
		Set("spiffe_id", req.SPIFFEID).
		Set("spiffe_prefix", req.SPIFFEPrefix).
		Set("agent_id", req.AgentID).
		Set("agent_template", req.AgentTemplate).
		Set("groups", req.Groups)
	defer func() { evt.Log(c) }()

	err := models.UpdateAgentSPIFFEMapping(m)
	evt.Err(err)
	switch {
	case errors.Is(err, models.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
	case err != nil && isValidationErr(err):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating spiffe mapping: %v", err)
	default:
		// re-read to return the freshest state
		got, gerr := models.GetAgentSPIFFEMapping(ctx.OrgID, m.ID)
		if gerr != nil {
			c.JSON(http.StatusOK, toOpenAPI(*m))
			return
		}
		c.JSON(http.StatusOK, toOpenAPI(*got))
	}
}

// Delete
//
//	@Summary		Delete SPIFFE Mapping
//	@Description	Remove a SPIFFE-ID to agent mapping.
//	@Tags			SPIFFE
//	@Produce		json
//	@Param			id		path		string	true	"Mapping ID"
//	@Success		204
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/spiffe-mappings/{id} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	id := c.Param("id")
	evt := audit.NewEvent(audit.ResourceAgentSPIFFEMapping, audit.ActionDelete).
		Resource(id, "")
	defer func() { evt.Log(c) }()

	err := models.DeleteAgentSPIFFEMapping(ctx.OrgID, id)
	evt.Err(err)
	switch {
	case errors.Is(err, models.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": err.Error()})
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting spiffe mapping: %v", err)
	default:
		c.Status(http.StatusNoContent)
	}
}

func toModel(req *openapi.AgentSPIFFEMapping, orgID string) *models.AgentSPIFFEMapping {
	var spiffeID, spiffePrefix, agentID, agentTemplate *string
	if req.SPIFFEID != "" {
		v := req.SPIFFEID
		spiffeID = &v
	}
	if req.SPIFFEPrefix != "" {
		v := req.SPIFFEPrefix
		spiffePrefix = &v
	}
	if req.AgentID != "" {
		v := req.AgentID
		agentID = &v
	}
	if req.AgentTemplate != "" {
		v := req.AgentTemplate
		agentTemplate = &v
	}
	return &models.AgentSPIFFEMapping{
		OrgID:         orgID,
		TrustDomain:   req.TrustDomain,
		SPIFFEID:      spiffeID,
		SPIFFEPrefix:  spiffePrefix,
		AgentID:       agentID,
		AgentTemplate: agentTemplate,
		Groups:        req.Groups,
	}
}

func toOpenAPI(m models.AgentSPIFFEMapping) openapi.AgentSPIFFEMapping {
	out := openapi.AgentSPIFFEMapping{
		ID:          m.ID,
		OrgID:       m.OrgID,
		TrustDomain: m.TrustDomain,
		Groups:      []string(m.Groups),
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
	if m.SPIFFEID != nil {
		out.SPIFFEID = *m.SPIFFEID
	}
	if m.SPIFFEPrefix != nil {
		out.SPIFFEPrefix = *m.SPIFFEPrefix
	}
	if m.AgentID != nil {
		out.AgentID = *m.AgentID
	}
	if m.AgentTemplate != nil {
		out.AgentTemplate = *m.AgentTemplate
	}
	return out
}

// labelFor produces a short human-readable label for audit events.
func labelFor(m openapi.AgentSPIFFEMapping) string {
	if m.SPIFFEID != "" {
		return m.SPIFFEID
	}
	return fmt.Sprintf("%s*", m.SPIFFEPrefix)
}

// isValidationErr returns true when the model layer returned a shape
// validation error that should be surfaced as 422 rather than 500.
func isValidationErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, sub := range []string{
		"is required",
		"must be set",
		"must start with",
		"exactly one of",
	} {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}
