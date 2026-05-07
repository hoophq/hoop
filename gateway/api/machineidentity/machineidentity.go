package machineidentityapi

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// ListMachineIdentities
//
//	@Summary		List Machine Identities
//	@Description	List all machine identities (non-human identities)
//	@Tags			Machine Identities
//	@Produce		json
//	@Success		200	{array}		openapi.MachineIdentity
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/machineidentities [get]
func List(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	items, err := models.ListMachineIdentities(ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing machine identities: %v", err)
		return
	}
	orgUUID, _ := uuid.Parse(ctx.OrgID)
	result := make([]openapi.MachineIdentity, 0, len(items))
	for _, item := range items {
		attrs, _ := models.GetMachineIdentityAttributes(models.DB, orgUUID, item.Name)
		result = append(result, toOpenAPI(item, attrs))
	}
	c.JSON(http.StatusOK, result)
}

// GetMachineIdentity
//
//	@Summary		Get Machine Identity
//	@Description	Get a machine identity by name
//	@Tags			Machine Identities
//	@Produce		json
//	@Param			name	path		string	true	"Machine Identity Name"
//	@Success		200		{object}	openapi.MachineIdentity
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/machineidentities/{name} [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	mi, err := models.GetMachineIdentityByName(ctx.OrgID, c.Param("name"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "machine identity not found"})
	case nil:
		orgUUID, _ := uuid.Parse(ctx.OrgID)
		attrs, _ := models.GetMachineIdentityAttributes(models.DB, orgUUID, mi.Name)
		c.JSON(http.StatusOK, toOpenAPI(*mi, attrs))
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching machine identity: %v", err)
	}
}

// CreateMachineIdentity
//
//	@Summary		Create Machine Identity
//	@Description	Create a machine identity (non-human identity) with native credentials for each connection
//	@Tags			Machine Identities
//	@Accept			json
//	@Produce		json
//	@Param			request	body		openapi.MachineIdentity	true	"The request body resource"
//	@Success		201		{object}	openapi.MachineIdentityCreateResponse
//	@Failure		400,409,422,500	{object}	openapi.HTTPError
//	@Router			/machineidentities [post]
func Create(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.MachineIdentity
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "name is required"})
		return
	}

	mi := &models.MachineIdentity{
		OrgID:           ctx.OrgID,
		Name:            req.Name,
		Description:     req.Description,
		ConnectionNames: req.ConnectionNames,
	}

	result, err := services.CreateMachineIdentity(context.Background(), mi, req.Attributes)
	switch err {
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": "machine identity with this name already exists"})
	case nil:
		resp := openapi.MachineIdentityCreateResponse{
			MachineIdentity: toOpenAPI(*result.Identity, result.Attributes),
			Credentials:     toCredentialResponses(result.Credentials),
		}
		c.JSON(http.StatusCreated, resp)
	default:
		httputils.AbortWithErr(c, http.StatusBadRequest, err, "failed creating machine identity: %v", err)
	}
}

// UpdateMachineIdentity
//
//	@Summary		Update Machine Identity
//	@Description	Update a machine identity. Adding connections provisions new credentials; removing connections revokes them.
//	@Tags			Machine Identities
//	@Accept			json
//	@Produce		json
//	@Param			name	path		string						true	"Machine Identity Name"
//	@Param			request	body		openapi.MachineIdentity		true	"The request body resource"
//	@Success		200		{object}	openapi.MachineIdentityUpdateResponse
//	@Failure		400,404,409,422,500	{object}	openapi.HTTPError
//	@Router			/machineidentities/{name} [put]
func Update(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var req openapi.MachineIdentity
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": "name is required"})
		return
	}

	result, err := services.UpdateMachineIdentity(
		context.Background(), ctx.OrgID, c.Param("name"),
		req.Name, req.Description,
		req.ConnectionNames, req.Attributes,
	)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "machine identity not found"})
	case models.ErrAlreadyExists:
		c.JSON(http.StatusConflict, gin.H{"message": "machine identity with this name already exists"})
	case nil:
		resp := openapi.MachineIdentityUpdateResponse{
			MachineIdentity: toOpenAPI(*result.Identity, result.Attributes),
			NewCredentials:  toCredentialResponses(result.NewCredentials),
		}
		c.JSON(http.StatusOK, resp)
	default:
		httputils.AbortWithErr(c, http.StatusBadRequest, err, "failed updating machine identity: %v", err)
	}
}

// DeleteMachineIdentity
//
//	@Summary		Delete Machine Identity
//	@Description	Delete a machine identity and revoke all associated credentials
//	@Tags			Machine Identities
//	@Param			name	path	string	true	"Machine Identity Name"
//	@Success		200
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/machineidentities/{name} [delete]
func Delete(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	err := services.DeleteMachineIdentity(context.Background(), ctx.OrgID, c.Param("name"))
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "machine identity not found"})
	case nil:
		c.JSON(http.StatusOK, gin.H{"message": "machine identity deleted"})
	default:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed deleting machine identity: %v", err)
	}
}

// ListCredentials
//
//	@Summary		List Machine Identity Credentials
//	@Description	List full credential details for a machine identity including connection strings and secret keys
//	@Tags			Machine Identities
//	@Produce		json
//	@Param			name	path		string	true	"Machine Identity Name"
//	@Success		200		{array}		openapi.MachineIdentityCredentialResponse
//	@Failure		404,500	{object}	openapi.HTTPError
//	@Router			/machineidentities/{name}/credentials [get]
func ListCredentials(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	identityName := c.Param("name")

	mi, err := models.GetMachineIdentityByName(ctx.OrgID, identityName)
	if err != nil {
		if err == models.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"message": "machine identity not found"})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed fetching machine identity: %v", err)
		return
	}

	micRows, err := models.ListMachineIdentityCredentials(ctx.OrgID, mi.ID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing credentials: %v", err)
		return
	}

	result := make([]openapi.MachineIdentityCredentialResponse, 0, len(micRows))
	for _, mic := range micRows {
		credInfo, err := services.GetMachineIdentityCredentialInfo(
			context.Background(), ctx.OrgID, identityName, mic.ConnectionName,
		)
		if err != nil {
			log.Warnf("failed fetching credential info for connection %s on MI %s: %v", mic.ConnectionName, identityName, err)
			continue
		}
		result = append(result, credentialInfoToResponse(credInfo))
	}
	c.JSON(http.StatusOK, result)
}

// RotateCredential
//
//	@Summary		Rotate Machine Identity Credential
//	@Description	Rotate (regenerate) a credential for a specific connection. The old credential is revoked and a new one is returned.
//	@Tags			Machine Identities
//	@Produce		json
//	@Param			name			path		string	true	"Machine Identity Name"
//	@Param			connectionName	path		string	true	"Connection Name"
//	@Success		201				{object}	openapi.MachineIdentityCredentialResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/machineidentities/{name}/credentials/{connectionName}/rotate [post]
func RotateCredential(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	identityName := c.Param("name")
	connName := c.Param("connectionName")

	credInfo, err := services.RotateMachineIdentityCredential(
		context.Background(), ctx.OrgID, identityName, connName,
	)
	switch err {
	case models.ErrNotFound:
		c.JSON(http.StatusNotFound, gin.H{"message": "machine identity or credential not found"})
	case nil:
		c.JSON(http.StatusCreated, credentialInfoToResponse(credInfo))
	default:
		httputils.AbortWithErr(c, http.StatusBadRequest, err, "failed rotating credential: %v", err)
	}
}

func toOpenAPI(mi models.MachineIdentity, attributes []string) openapi.MachineIdentity {
	if attributes == nil {
		attributes = []string{}
	}
	return openapi.MachineIdentity{
		ID:              mi.ID,
		Name:            mi.Name,
		Description:     mi.Description,
		ConnectionNames: mi.ConnectionNames,
		Attributes:      attributes,
		CreatedAt:       &mi.CreatedAt,
		UpdatedAt:       &mi.UpdatedAt,
	}
}

func toCredentialResponses(credentials []*services.CredentialInfo) []openapi.MachineIdentityCredentialResponse {
	var result []openapi.MachineIdentityCredentialResponse
	for _, cred := range credentials {
		result = append(result, credentialInfoToResponse(cred))
	}
	return result
}

func credentialInfoToResponse(info *services.CredentialInfo) openapi.MachineIdentityCredentialResponse {
	resp := openapi.MachineIdentityCredentialResponse{
		ConnectionName:    info.ConnectionName,
		ConnectionType:    info.ConnectionType,
		ConnectionSubType: info.ConnectionSubType,
		SecretKey:         info.SecretKey,
		Hostname:          info.Hostname,
		Port:              info.Port,
	}
	if info.Postgres != nil {
		resp.DatabaseName = info.Postgres.DatabaseName
		resp.ConnectionString = info.Postgres.ConnectionString
		resp.Username = info.SecretKey
		resp.Password = "hoop"
	}
	if info.SSH != nil {
		resp.Command = info.SSH.Command
		resp.Username = "hoop"
		resp.Password = info.SecretKey
	}
	if info.RDP != nil {
		resp.Command = info.RDP.Command
		resp.Username = info.SecretKey
		resp.Password = info.SecretKey
	}
	if info.SSM != nil {
		resp.EndpointURL = info.SSM.EndpointURL
		resp.AwsAccessKeyId = info.SSM.AwsAccessKeyId
		resp.AwsSecretAccessKey = info.SSM.AwsSecretAccessKey
		resp.ConnectionString = info.SSM.ConnectionString
	}
	if info.HTTPProxy != nil {
		resp.ProxyToken = info.HTTPProxy.ProxyToken
		resp.Command = info.HTTPProxy.Command
	}
	return resp
}
