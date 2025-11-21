package search

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/common/runbooks"
	"github.com/hoophq/hoop/gateway/api/openapi"
	runbookapi "github.com/hoophq/hoop/gateway/api/runbooks"
	"github.com/hoophq/hoop/gateway/models"
)

const RUNBOOK_CACHE_TTL = 10 * time.Minute

var runbookCacheStore = memory.New()

type runbookCache struct {
	Files     []*openapi.RunbookSearch
	CreatedAt time.Time
}

func fetchRunbookFilesWithCache(orgID string, config *models.Runbooks) ([]*openapi.RunbookSearch, error) {
	// Check cache first
	if cacheEntry := runbookCacheStore.Get(orgID); cacheEntry != nil {
		cachedRunbook := cacheEntry.(*runbookCache)
		if time.Since(cachedRunbook.CreatedAt) < RUNBOOK_CACHE_TTL {
			return cachedRunbook.Files, nil
		}
	}

	// Fetch runbook repository configurations for the organization
	runbookFiles := make([]*openapi.RunbookSearch, 0)
	for _, repoConfig := range config.RepositoryConfigs {
		// Build config from repository configuration
		config, err := models.BuildCommonConfig(&repoConfig)
		if err != nil {
			return nil, err
		}

		// Fetch the latest commit from the repository
		commit, err := runbookapi.GetRunbooks(orgID, config)
		if err != nil {
			return nil, err
		}

		ctree, _ := commit.Tree()
		if ctree == nil {
			return nil, nil
		}

		// Iterate through files in the repository
		repository := config.GetNormalizedGitURL()
		err = ctree.Files().ForEach(func(f *object.File) error {
			if !runbooks.IsRunbookFile(f.Name) {
				return nil
			}

			runbookFiles = append(runbookFiles, &openapi.RunbookSearch{
				Repository: repository,
				Name:       f.Name,
			})

			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// Cache the fetched runbook files
	cacheEntry := &runbookCache{
		Files:     runbookFiles,
		CreatedAt: time.Now(),
	}
	runbookCacheStore.Set(orgID, cacheEntry)

	return runbookFiles, nil
}

func filterRunbookFilesByPath(files []*openapi.RunbookSearch, search string) []*openapi.RunbookSearch {
	normalizedSearch := strings.ToLower(search)
	matchingFiles := make([]*openapi.RunbookSearch, 0)

	for _, file := range files {
		fullPath := strings.ToLower(fmt.Sprintf("%s/%s", file.Repository, file.Name))

		if strings.Contains(fullPath, normalizedSearch) {
			matchingFiles = append(matchingFiles, file)
		}
	}

	return matchingFiles
}

func findRunbookFilesByPath(orgID string, config *models.Runbooks, search string) ([]*openapi.RunbookSearch, error) {
	files, err := fetchRunbookFilesWithCache(orgID, config)
	if err != nil {
		return nil, err
	}

	matchingFiles := filterRunbookFilesByPath(files, search)

	return matchingFiles, nil
}
