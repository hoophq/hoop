package search

import (
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/common/runbooks"
)

const RUNBOOK_CACHE_TTL = 5 * time.Minute

type RunbookCache struct {
	GitURL    string
	Commit    *object.Commit
	CreatedAt time.Time
}

var runbookCacheStore = memory.New()

func getRunbookCommit(config *runbooks.Config, orgID string) (*object.Commit, error) {
	// Try to get the cached commit
	cache, _ := runbookCacheStore.Get(orgID).(*RunbookCache)
	if cache != nil {
		// Check if the cache is still valid and GitURL matches
		if time.Since(cache.CreatedAt) < RUNBOOK_CACHE_TTL && cache.GitURL == config.GitURL {
			return cache.Commit, nil
		}
	}

	commit, err := runbooks.CloneRepositoryInMemory(config)
	if err != nil {
		return nil, err
	}

	runbookCacheStore.Set(orgID, &RunbookCache{
		GitURL:    config.GitURL,
		Commit:    commit,
		CreatedAt: time.Now().UTC(),
	})

	return commit, nil
}

func findRunbookFilesByPath(path string, config *runbooks.Config, orgID string) ([]string, error) {
	commit, err := getRunbookCommit(config, orgID)
	if err != nil {
		return nil, err
	}

	var runbookPaths []string

	ctree, _ := commit.Tree()
	if ctree == nil {
		return runbookPaths, nil
	}
	if path == "" {
		return runbookPaths, nil
	}

	normalizedPath := strings.ToLower(path)

	return runbookPaths, ctree.Files().ForEach(func(f *object.File) error {
		if !runbooks.IsRunbookFile(f.Name) {
			return nil
		}

		if !strings.Contains(strings.ToLower(f.Name), normalizedPath) {
			return nil
		}

		runbookPaths = append(runbookPaths, f.Name)

		return nil
	})
}
