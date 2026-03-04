package search

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	"golang.org/x/sync/errgroup"
)

// Search
//
//	@Summary		Search
//	@Description	Performs a search for connections and runbooks based on the provided criteria.
//	@Tags			Search
//	@Produce		json
//	@Param			term		query		string	true	"Search term"
//	@Success		200			{object}	openapi.SearchResponse
//	@Failure		400,422,500	{object}	openapi.HTTPError
//	@Router			/search [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	searchTerm := c.Query("term")
	if searchTerm == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "term parameter is required"})
		return
	}

	var (
		connectionsFound []models.Connection
		resourcesFound   []models.Resources
		runbooksFound    []*openapi.RunbookSearch

		errors []error
	)

	g, _ := errgroup.WithContext(c)

	// Fetch connections
	g.Go(func() error {
		var err error
		connectionsFound, err = models.SearchConnectionsBySimilarity(ctx.GetOrgID(), ctx.GetUserGroups(), searchTerm)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed fetching connections, reason=%v", err))
		}

		return err
	})

	// Fetch runbooks
	g.Go(func() error {
		config, err := models.GetRunbookConfigurationByOrgID(models.DB, ctx.GetOrgID())
		if err != nil {
			errors = append(errors, fmt.Errorf("failed getting the runbooks plugin, reason=%v", err))
			return err
		}

		runbooksFound, err = findRunbookFilesByPath(ctx.GetOrgID(), config, searchTerm)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed searching runbooks, reason=%v", err))
		}
		return err
	})

	// Fetch resources
	g.Go(func() error {
		var err error
		opts := models.ResourceFilterOption{
			Search:   searchTerm,
			Page:     1,
			PageSize: 0,
		}

		resourcesFound, _, err = models.ListResources(models.DB, ctx.OrgID, ctx.UserGroups, ctx.IsAdmin(), opts)
		if err != nil {
			errors = append(errors, fmt.Errorf("failed listing resources, reason=%v", err))
		}

		return err
	})

	// Wait for the goroutines
	if err := g.Wait(); err != nil {
		log.Error(err)
	}

	// Build response
	c.JSON(http.StatusOK, buildSearchResponse(connectionsFound, runbooksFound, resourcesFound, errors))
}

func buildSearchResponse(connections []models.Connection, runbooks []*openapi.RunbookSearch, resources []models.Resources, errors []error) openapi.SearchResponse {
	connectionSearchResults := make([]openapi.ConnectionSearch, len(connections))
	for i, conn := range connections {
		connectionSearchResults[i] = connectionToConnectionSearch(&conn)
	}

	resourcesSearchResults := make([]openapi.ResourceSearch, len(resources))
	for i, res := range resources {
		resourcesSearchResults[i] = resourceToResourceSearch(&res)
	}

	var errorMessages []string
	for _, err := range errors {
		errorMessages = append(errorMessages, err.Error())
	}

	return openapi.SearchResponse{
		Errors:      errorMessages,
		Connections: connectionSearchResults,
		Runbooks:    runbooks,
		Resources:   resourcesSearchResults,
	}
}

func resourceToResourceSearch(res *models.Resources) openapi.ResourceSearch {
	return openapi.ResourceSearch{
		ID:      res.ID,
		Name:    res.Name,
		Type:    res.Type,
		SubType: res.SubType.String,
	}
}

func connectionToConnectionSearch(conn *models.Connection) openapi.ConnectionSearch {
	return openapi.ConnectionSearch{
		ID:                 conn.ID,
		Name:               conn.Name,
		ResourceName:       conn.ResourceName,
		Status:             conn.Status,
		Type:               conn.Type,
		SubType:            conn.SubType.String,
		AccessModeRunbooks: conn.AccessModeRunbooks,
		AccessModeExec:     conn.AccessModeExec,
		AccessModeConnect:  conn.AccessModeConnect,
	}
}
