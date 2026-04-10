package agentidentities

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"golang.org/x/crypto/bcrypt"
)

// List godoc
//
//	@Summary		List Agent Identities
//	@Description	List all agent identities in the organization
//	@Tags			User Management
//	@Produce		json
//	@Success		200	{array}		openapi.AgentIdentity
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/agentidentities [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	items, err := models.ListAgentIdentities(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing agent identities: %v", err)
		return
	}
	resp := make([]openapi.AgentIdentity, 0, len(items))
	for _, item := range items {
		resp = append(resp, toOpenAPI(item))
	}
	c.JSON(http.StatusOK, resp)
}

// Get godoc
//
//	@Summary		Get Agent Identity
//	@Description	Get an agent identity by ID
//	@Tags			User Management
//	@Produce		json
//	@Param			id	path		string	true	"Agent Identity ID"
//	@Success		200	{object}	openapi.AgentIdentity
//	@Failure		404	{object}	openapi.HTTPError
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/agentidentities/{id} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	item, err := models.GetAgentIdentityByID(ctx.OrgID, c.Param("id"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": models.ErrNotFound.Error()})
	case nil:
		c.JSON(http.StatusOK, toOpenAPI(*item))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching agent identity: %v", err)
	}
}

// Create godoc
//
//	@Summary		Create Agent Identity
//	@Description	Create a new agent identity
//	@Tags			User Management
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.AgentIdentity	true	"The request body resource"
//	@Success		201		{object}	openapi.AgentIdentity
//	@Failure		400		{object}	openapi.HTTPError
//	@Failure		409		{object}	openapi.HTTPError
//	@Failure		422		{object}	openapi.HTTPError
//	@Failure		500		{object}	openapi.HTTPError
//	@Router			/agentidentities [post]
func Create(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.AgentIdentity
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.Subject == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "subject is required"})
		return
	}
	if req.Status != openapi.AgentIdentityStatusActive && req.Status != openapi.AgentIdentityStatusInactive {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("invalid status %q", req.Status)})
		return
	}
	a := &models.AgentIdentity{
		ID:      uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("agentidentity/%s/%s", ctx.OrgID, req.Subject))).String(),
		OrgID:   ctx.OrgID,
		Subject: req.Subject,
		Name:    req.Name,
		Groups:  req.Groups,
		Status:  string(req.Status),
	}
	err := models.CreateAgentIdentity(a)
	switch err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": models.ErrAlreadyExists.Error()})
	case nil:
		c.JSON(http.StatusCreated, toOpenAPI(*a))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating agent identity: %v", err)
	}
}

// Update godoc
//
//	@Summary		Update Agent Identity
//	@Description	Update an existing agent identity
//	@Tags			User Management
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Agent Identity ID"
//	@Param			request	body		openapi.AgentIdentity	true	"The request body resource"
//	@Success		200		{object}	openapi.AgentIdentity
//	@Failure		400		{object}	openapi.HTTPError
//	@Failure		404		{object}	openapi.HTTPError
//	@Failure		422		{object}	openapi.HTTPError
//	@Failure		500		{object}	openapi.HTTPError
//	@Router			/agentidentities/{id} [put]
func Update(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.AgentIdentity
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.Status != openapi.AgentIdentityStatusActive && req.Status != openapi.AgentIdentityStatusInactive {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("invalid status %q", req.Status)})
		return
	}
	a := &models.AgentIdentity{
		ID:     c.Param("id"),
		OrgID:  ctx.OrgID,
		Name:   req.Name,
		Groups: req.Groups,
		Status: string(req.Status),
	}
	err := models.UpdateAgentIdentity(a)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": models.ErrNotFound.Error()})
	case nil:
		c.JSON(http.StatusOK, toOpenAPI(*a))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating agent identity: %v", err)
	}
}

// Delete godoc
//
//	@Summary		Delete Agent Identity
//	@Description	Delete an agent identity and all its secrets
//	@Tags			User Management
//	@Produce		json
//	@Param			id	path	string	true	"Agent Identity ID"
//	@Success		204
//	@Failure		404	{object}	openapi.HTTPError
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/agentidentities/{id} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	err := models.DeleteAgentIdentity(ctx.OrgID, c.Param("id"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": models.ErrNotFound.Error()})
	case nil:
		c.Status(http.StatusNoContent)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting agent identity: %v", err)
	}
}

// ListSecrets godoc
//
//	@Summary		List Agent Identity Secrets
//	@Description	List all secrets for an agent identity (tokens are never returned)
//	@Tags			User Management
//	@Produce		json
//	@Param			id	path		string	true	"Agent Identity ID"
//	@Success		200	{array}		openapi.AgentIdentitySecret
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/agentidentities/{id}/secrets [get]
func ListSecrets(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	items, err := models.ListAgentIdentitySecrets(ctx.OrgID, c.Param("id"))
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing agent identity secrets: %v", err)
		return
	}
	resp := make([]openapi.AgentIdentitySecret, 0, len(items))
	for _, s := range items {
		resp = append(resp, secretToOpenAPI(s, ""))
	}
	c.JSON(http.StatusOK, resp)
}

// CreateSecret godoc
//
//	@Summary		Create Agent Identity Secret
//	@Description	Create a new secret for an agent identity. The raw token is returned only once.
//	@Tags			User Management
//	@Produce		json
//	@Param			id	path		string	true	"Agent Identity ID"
//	@Success		201	{object}	openapi.AgentIdentitySecret
//	@Failure		404	{object}	openapi.HTTPError
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/agentidentities/{id}/secrets [post]
func CreateSecret(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	agentIdentityID := c.Param("id")

	// verify the agent identity exists and belongs to this org
	if _, err := models.GetAgentIdentityByID(ctx.OrgID, agentIdentityID); err != nil {
		if err == models.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": models.ErrNotFound.Error()})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching agent identity: %v", err)
		return
	}

	rawToken, hashedSecret, err := generateAgentToken()
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed generating agent token: %v", err)
		return
	}

	secret := &models.AgentIdentitySecret{
		ID:              uuid.NewString(),
		AgentIdentityID: agentIdentityID,
		KeyPrefix:       rawToken[:8],
		HashedSecret:    hashedSecret,
	}
	if err := models.CreateAgentIdentitySecret(secret); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating agent identity secret: %v", err)
		return
	}
	c.JSON(http.StatusCreated, secretToOpenAPI(*secret, rawToken))
}

// DeleteSecret godoc
//
//	@Summary		Delete Agent Identity Secret
//	@Description	Revoke a specific secret for an agent identity
//	@Tags			User Management
//	@Produce		json
//	@Param			id			path	string	true	"Agent Identity ID"
//	@Param			secret_id	path	string	true	"Secret ID"
//	@Success		204
//	@Failure		404	{object}	openapi.HTTPError
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/agentidentities/{id}/secrets/{secret_id} [delete]
func DeleteSecret(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	err := models.DeleteAgentIdentitySecret(ctx.OrgID, c.Param("secret_id"), c.Param("id"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": models.ErrNotFound.Error()})
	case nil:
		c.Status(http.StatusNoContent)
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting agent identity secret: %v", err)
	}
}

func toOpenAPI(a models.AgentIdentity) openapi.AgentIdentity {
	return openapi.AgentIdentity{
		ID:        a.ID,
		OrgID:     a.OrgID,
		Subject:   a.Subject,
		Name:      a.Name,
		Status:    openapi.AgentIdentityStatusType(a.Status),
		Groups:    a.Groups,
		CreatedAt: a.CreatedAt.Format(time.RFC3339),
	}
}

func secretToOpenAPI(s models.AgentIdentitySecret, rawToken string) openapi.AgentIdentitySecret {
	resp := openapi.AgentIdentitySecret{
		ID:        s.ID,
		KeyPrefix: s.KeyPrefix,
		CreatedAt: s.CreatedAt.Format(time.RFC3339),
		Token:     rawToken,
	}
	if s.ExpiresAt != nil {
		t := s.ExpiresAt.Format(time.RFC3339)
		resp.ExpiresAt = &t
	}
	return resp
}

// generateAgentToken creates a new agt- prefixed token and returns (rawToken, hashedSecret, error).
func generateAgentToken() (string, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("failed generating random bytes: %v", err)
	}
	rawToken := "agt-" + base64.RawURLEncoding.EncodeToString(b)
	hashed, err := bcrypt.GenerateFromPassword([]byte(rawToken), bcrypt.DefaultCost)
	if err != nil {
		return "", "", fmt.Errorf("failed hashing token: %v", err)
	}
	return rawToken, string(hashed), nil
}
