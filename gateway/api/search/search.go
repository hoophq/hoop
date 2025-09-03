package search

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/runbooks"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"golang.org/x/sync/errgroup"
)

// Search
//
//	@Summary		Search
//	@Description	Performs a search for connections and runbooks based on the provided criteria.
//	@Tags			Search
//	@Produce		json
//	@Param			term	query	string	true	"Search term"
//	@Success		200 {object} openapi.SearchResponse
//	@Failure		400,422,500	{object} openapi.HTTPError
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
		runbooksFound    []string

		runbookErr error
	)

	g, _ := errgroup.WithContext(c)

	// Fetch connections in parallel
	g.Go(func() error {
		var err error
		connectionsFound, err = models.SearchConnectionsBySimilarity(ctx.GetOrgID(), ctx.GetUserGroups(), searchTerm)
		if err != nil {
			return fmt.Errorf("failed fetching connections: %w", err)
		}
		return nil
	})

	// Fetch runbooks in parallel
	g.Go(func() error {
		p, err := models.GetPluginByName(ctx.GetOrgID(), plugintypes.PluginRunbooksName)
		if err != nil {
			log.Infof("failed on getting the runbooks plugin, err=%v", err)
			return nil
		}

		var configEnvVars map[string]string
		if p.EnvVars != nil {
			configEnvVars = p.EnvVars
		}

		config, err := runbooks.NewConfig(configEnvVars)
		if err != nil {
			log.Infof("failed on create config for runbooks, err=%v", err)
			return nil
		}

		runbooksFound, err = findRunbookFilesByPath(searchTerm, config, ctx.GetOrgID())
		if err != nil {
			log.Infof("failed listing runbooks, err=%v", err)
			runbookErr = fmt.Errorf("failed searching runbooks, reason=%v", err)
		}
		return nil
	})

	// Wait for both goroutines
	if err := g.Wait(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": err.Error()})
		return
	}

	// Handle runbook error separately
	if runbookErr != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": runbookErr.Error()})
		return
	}

	// Build response
	c.JSON(http.StatusOK, buildSearchResponse(connectionsFound, runbooksFound))
}

func buildSearchResponse(connections []models.Connection, runbooks []string) openapi.SearchResponse {
	var connectionSearchResults []openapi.ConnectionSearch
	for _, conn := range connections {
		connectionSearchResults = append(connectionSearchResults, connectionToConnectionSearch(&conn))
	}

	return openapi.SearchResponse{
		Connections: connectionSearchResults,
		Runbooks:    runbooks,
	}
}

func connectionToConnectionSearch(conn *models.Connection) openapi.ConnectionSearch {
	return openapi.ConnectionSearch{
		ID:                 conn.ID,
		Name:               conn.Name,
		Status:             conn.Status,
		Type:               conn.Type,
		SubType:            conn.SubType.String,
		AccessModeRunbooks: conn.AccessModeRunbooks,
		AccessModeExec:     conn.AccessModeExec,
		AccessModeConnect:  conn.AccessModeConnect,
	}
}
