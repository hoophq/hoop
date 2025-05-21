package apirunbooks

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/hoophq/hoop/common/runbooks"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
)

const maxTemplateSize = 1000000 // 1MB

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

func getAccessToken(c *gin.Context) string {
	tokenHeader := c.GetHeader("authorization")
	apiKey := c.GetHeader("Api-Key")
	if apiKey != "" {
		return apiKey
	}
	tokenParts := strings.Split(tokenHeader, " ")
	if len(tokenParts) > 1 {
		return tokenParts[1]
	}
	return ""
}
