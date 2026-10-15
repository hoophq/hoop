package apiagents

import (
	"errors"
	"net/http"
	"sort"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apivalidation "github.com/hoophq/hoop/gateway/api/validation"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

// predefinedTags are the agent tag suggestions of every org. They are a subset
// of the connection system tags, with the same keys and values.
var predefinedTags = map[string][]string{
	"hoop.dev/infrastructure.cloud":       {"aws", "gcp", "azure", "digitalocean", "cloudflare"},
	"hoop.dev/infrastructure.environment": {"prod", "staging", "dev", "qa", "sandbox"},
	"hoop.dev/organization.team":          {"backend", "frontend", "platform", "devops", "security", "data"},
	"hoop.dev/organization.department":    {"engineering", "marketing", "sales", "finance"},
	"hoop.dev/operations.criticality":     {"critical", "important", "normal"},
}

// UpdateAgentTags
//
//	@Summary		Update Agent Tags
//	@Description	Replace the tags of an agent. Send `{}` to remove all tags. No other attribute changes.
//	@Tags			Agents
//	@Accept			json
//	@Produce		json
//	@Param			nameOrID	path		string				true	"The name or ID of the resource"
//	@Param			request		body		map[string]string	true	"The tags of the agent"
//	@Success		200			{object}	openapi.AgentResponse
//	@Failure		400,404,500	{object}	openapi.HTTPError
//	@Router			/agents/{nameOrID}/tags [put]
func UpdateTags(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	var tags map[string]string
	if err := c.ShouldBindJSON(&tags); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if tags == nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "the request body must be an object of tags, send {} to remove all tags"})
		return
	}
	if err := apivalidation.ValidateTags("tags", tags); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	agent, err := models.GetAgentByNameOrID(ctx.OrgID, c.Param("nameOrID"))
	if errors.Is(err, models.ErrNotFound) || (err == nil && agent.Mode == proto.AgentModeMultiConnectionType) {
		// multi-connection keys are not listed by this API, see Get
		c.JSON(http.StatusNotFound, gin.H{"message": "agent not found"})
		return
	}
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed getting agent: %v", err)
		return
	}

	err = models.UpdateAgentTags(models.DB, ctx.OrgID, agent.ID, tags)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "agent not found"})
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed updating agent tags: %v", err)
	default:
		c.JSON(http.StatusOK, toAgentResponse(*agent, tags))
	}
}

// ListAgentTags
//
//	@Summary		List Agent Tags
//	@Description	List the agent tag suggestions: the predefined tags and every key/value in use on the agents of the organization.
//	@Tags			Agents
//	@Produce		json
//	@Success		200	{object}	openapi.AgentTagList
//	@Failure		500	{object}	openapi.HTTPError
//	@Router			/agent-tags [get]
func ListTags(c *gin.Context) {
	ctx := storagev2.ParseContext(c)
	inUse, err := models.ListAgentTagsInUse(models.DB, ctx.OrgID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing agent tags: %v", err)
		return
	}
	c.JSON(http.StatusOK, openapi.AgentTagList{Items: tagSuggestions(inUse)})
}

// tagSuggestions merges the predefined tags with the tags in use, without
// duplicates, sorted by key and value.
func tagSuggestions(inUse []models.AgentTag) []openapi.AgentTag {
	seen := map[openapi.AgentTag]bool{}
	items := []openapi.AgentTag{}
	add := func(t openapi.AgentTag) {
		if !seen[t] {
			seen[t] = true
			items = append(items, t)
		}
	}
	for key, values := range predefinedTags {
		for _, val := range values {
			add(openapi.AgentTag{Key: key, Value: val})
		}
	}
	for _, t := range inUse {
		add(openapi.AgentTag{Key: t.Key, Value: t.Value})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Key != items[j].Key {
			return items[i].Key < items[j].Key
		}
		return items[i].Value < items[j].Value
	})
	return items
}
