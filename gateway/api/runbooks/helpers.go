package apirunbooks

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/runbooks"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

const maxTemplateSize = 1_000_000 // 1MB

const cacheDuration = 5 * time.Minute

type runbookCache struct {
	commit   *object.Commit
	cachedAt time.Time
}

var runbooksCache sync.Map // sync.Map[orgId]map[gitUrl]*runbookCache

func setRunbookCache(orgId, gitUrl string, commit *object.Commit) {
	var inner map[string]*runbookCache

	v, ok := runbooksCache.Load(orgId)
	if ok {
		inner, ok = v.(map[string]*runbookCache)
		if !ok {
			log.Errorf("invalid runbook cache structure for orgId=%s", orgId)
			inner = make(map[string]*runbookCache)
		}
	} else {
		inner = make(map[string]*runbookCache)
		runbooksCache.Store(orgId, inner)
	}

	inner[gitUrl] = &runbookCache{
		commit:   commit,
		cachedAt: time.Now().UTC(),
	}
}

func GetRunbookCache(orgId, gitUrl string) (*object.Commit, bool) {
	if v, ok := runbooksCache.Load(orgId); ok {
		inner, ok := v.(map[string]*runbookCache)
		if !ok {
			log.Infof("invalid runbook cache structure for orgId=%s", orgId)
			return nil, false
		}

		rb, ok := inner[gitUrl]
		if !ok {
			return nil, false
		}

		// Invalidate cache if expired
		if time.Since(rb.cachedAt) > cacheDuration {
			delete(inner, gitUrl)
			runbooksCache.Store(orgId, inner)
			return nil, false
		}

		return rb.commit, ok
	}
	return nil, false
}

func deleteRunbookCache(orgId string, gitUrl string) {
	if gitUrl == "" {
		runbooksCache.Delete(orgId)
		return
	}

	if v, ok := runbooksCache.Load(orgId); ok {
		inner, ok := v.(map[string]*runbookCache)
		if !ok {
			log.Errorf("invalid runbook cache structure for orgId=%s", orgId)
			return
		}
		delete(inner, gitUrl)
		runbooksCache.Store(orgId, inner)
	}
}

func slicesHasIntersection[T comparable](a, b []T) bool {
	// Ensure 'a' is the smaller slice to optimize performance
	if len(a) > len(b) {
		a, b = b, a
	}

	return slices.ContainsFunc(a, func(x T) bool {
		return slices.Contains(b, x)
	})
}

func getRunbookConnections(runbookRules []models.RunbookRules, connectionList []string, runbookRepository, runbookName string, userGroups []string) []string {
	// If no connections available, return empty list
	if len(connectionList) == 0 {
		return []string{}
	}

	// If no runbook rules defined, return all connections
	if len(runbookRules) == 0 {
		return connectionList
	}

	// If user is admin, return all connections
	isAdmin := slices.Contains(userGroups, types.GroupAdmin)
	if isAdmin {
		return connectionList
	}

	var matchedRules []models.RunbookRules
	for _, rule := range runbookRules {
		// Check if user groups intersect with rule user groups
		hasMatchingUserGroup := len(rule.UserGroups) == 0 || slicesHasIntersection(rule.UserGroups, userGroups)

		// Check if runbook is listed in the rule
		// Only runs if no matching user group found
		hasMatchingRunbook := !hasMatchingUserGroup && (len(rule.Runbooks) == 0 || slices.ContainsFunc(rule.Runbooks, func(runbook models.RunbookRuleFile) bool {
			return runbook.Repository == runbookRepository && runbook.Name == runbookName
		}))

		if hasMatchingUserGroup || hasMatchingRunbook {
			if len(rule.Connections) == 0 {
				return connectionList
			}

			matchedRules = append(matchedRules, rule)
		}
	}

	// Aggregate unique connections from matched rules
	connectionsMap := make(map[string]struct{})
	for _, rule := range matchedRules {
		for _, conn := range rule.Connections {
			connectionsMap[conn] = struct{}{}
		}
	}

	// Transform map keys to slice
	connections := make([]string, 0, len(connectionsMap))
	for conn := range connectionsMap {
		connections = append(connections, conn)
	}

	return connections
}

func listRunbookFilesV2(orgId string, config *runbooks.Config, rules []models.RunbookRules, connectionList, userGroups []string, removeEmptyConnections bool) (*openapi.RunbookRepositoryList, error) {
	commit, ok := GetRunbookCache(orgId, config.GetNormalizedGitURL())

	if !ok {
		var err error
		commit, err = runbooks.CloneRepositoryInMemory(config)
		if err != nil {
			return nil, err
		}

		setRunbookCache(orgId, config.GetNormalizedGitURL(), commit)
	}

	runbookList := &openapi.RunbookRepositoryList{
		Repository:    config.GetNormalizedGitURL(),
		Commit:        commit.Hash.String(),
		CommitAuthor:  commit.Author.String(),
		CommitMessage: commit.Message,
		Items:         []*openapi.Runbook{},
	}
	ctree, _ := commit.Tree()
	if ctree == nil {
		return runbookList, nil
	}

	return runbookList, ctree.Files().ForEach(func(f *object.File) error {
		if !runbooks.IsRunbookFile(f.Name) {
			return nil
		}

		connectionList := getRunbookConnections(rules, connectionList, config.GetNormalizedGitURL(), f.Name, userGroups)
		if removeEmptyConnections && len(connectionList) == 0 {
			return nil
		}

		runbook := &openapi.Runbook{
			Name:           f.Name,
			Metadata:       map[string]any{},
			ConnectionList: connectionList,
			Error:          nil,
		}
		blobData, err := runbooks.ReadBlob(f)
		if err != nil {
			runbook.Error = toPtrStr(err)
			runbookList.Items = append(runbookList.Items, runbook)
			return nil
		}
		if len(blobData) > maxTemplateSize {
			runbook.Error = toPtrStr(fmt.Errorf("max template size [%v KB] reached", maxTemplateSize/1000))
			runbookList.Items = append(runbookList.Items, runbook)
			return nil
		}
		t, err := runbooks.Parse(string(blobData))
		if err != nil {
			runbook.Error = toPtrStr(fmt.Errorf("template parse error: %v", err))
			runbookList.Items = append(runbookList.Items, runbook)
			return nil
		}
		runbook.Metadata = t.Attributes()
		runbookList.Items = append(runbookList.Items, runbook)
		return nil
	})
}

func listRunbookFiles(pluginConnectionList []*models.PluginConnection, config *runbooks.Config) (*openapi.RunbookList, error) {
	commit, err := runbooks.CloneRepositoryInMemory(config)
	if err != nil {
		return nil, err
	}
	runbookList := &openapi.RunbookList{
		Commit:        commit.Hash.String(),
		CommitAuthor:  commit.Author.String(),
		CommitMessage: commit.Message,
		Items:         []*openapi.Runbook{},
	}
	ctree, _ := commit.Tree()
	if ctree == nil {
		return runbookList, nil
	}
	return runbookList, ctree.Files().ForEach(func(f *object.File) error {
		if !runbooks.IsRunbookFile(f.Name) {
			return nil
		}
		var connectionList []string
		for _, conn := range pluginConnectionList {
			if len(conn.Config) == 0 {
				connectionList = append(connectionList, conn.ConnectionName)
				continue
			}
			pathPrefix := conn.Config[0]
			if pathPrefix != "" && strings.HasPrefix(f.Name, pathPrefix) {
				connectionList = append(connectionList, conn.ConnectionName)
			}
		}

		runbook := &openapi.Runbook{
			Name:           f.Name,
			Metadata:       map[string]any{},
			ConnectionList: connectionList,
			Error:          nil,
		}
		blobData, err := runbooks.ReadBlob(f)
		if err != nil {
			runbook.Error = toPtrStr(err)
			runbookList.Items = append(runbookList.Items, runbook)
			return nil
		}
		if len(blobData) > maxTemplateSize {
			runbook.Error = toPtrStr(fmt.Errorf("max template size [%v KB] reached", maxTemplateSize/1000))
			runbookList.Items = append(runbookList.Items, runbook)
			return nil
		}
		t, err := runbooks.Parse(string(blobData))
		if err != nil {
			runbook.Error = toPtrStr(fmt.Errorf("template parse error: %v", err))
			runbookList.Items = append(runbookList.Items, runbook)
			return nil
		}
		runbook.Metadata = t.Attributes()
		runbookList.Items = append(runbookList.Items, runbook)
		return nil
	})
}

func listRunbookFilesByPathPrefix(pathPrefix string, config *runbooks.Config) (*openapi.RunbookList, error) {
	commit, err := runbooks.CloneRepositoryInMemory(config)
	if err != nil {
		return nil, err
	}
	runbookList := &openapi.RunbookList{
		Commit:        commit.Hash.String(),
		CommitAuthor:  commit.Author.String(),
		CommitMessage: commit.Message,
		Items:         []*openapi.Runbook{},
	}
	ctree, _ := commit.Tree()
	if ctree == nil {
		return runbookList, nil
	}
	return runbookList, ctree.Files().ForEach(func(f *object.File) error {
		if !runbooks.IsRunbookFile(f.Name) {
			return nil
		}
		if pathPrefix != "" && !strings.HasPrefix(f.Name, pathPrefix) {
			return nil
		}
		runbook := &openapi.Runbook{
			Name:           f.Name,
			Metadata:       map[string]any{},
			ConnectionList: nil,
			Error:          nil,
		}
		blobData, err := runbooks.ReadBlob(f)
		if err != nil {
			runbook.Error = toPtrStr(err)
			runbookList.Items = append(runbookList.Items, runbook)
			return nil
		}
		if len(blobData) > maxTemplateSize {
			runbook.Error = toPtrStr(fmt.Errorf("max template size [%v KB] reached", maxTemplateSize/1000))
			runbookList.Items = append(runbookList.Items, runbook)
			return nil
		}
		t, err := runbooks.Parse(string(blobData))
		if err != nil {
			runbook.Error = toPtrStr(fmt.Errorf("template parse error: %v", err))
			runbookList.Items = append(runbookList.Items, runbook)
			return nil
		}
		runbook.ConnectionList = nil
		runbook.Metadata = t.Attributes()
		runbookList.Items = append(runbookList.Items, runbook)
		return nil
	})
}

func toPtrStr(v any) *string {
	if v == nil || fmt.Sprintf("%v", v) == "" {
		return nil
	}
	val := fmt.Sprintf("%v", v)
	return &val
}
