package apirunbooks

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/api/runbooks/templates"
	"github.com/hoophq/hoop/gateway/storagev2/types"
)

const maxTemplateSize = 1000000 // 1MB

func FetchRunbookFile(config *templates.RunbookConfig, fileName, refHash string, parameters map[string]string) (*openapi.Runbook, error) {
	c, err := templates.FetchRepo(config)
	if err != nil {
		return nil, err
	}
	if c.Hash.IsZero() {
		return nil, fmt.Errorf("commit hash from remote is empty")
	}
	if refHash != "" && refHash != c.Hash.String() {
		return nil, fmt.Errorf("mismatch git commit, want=%v, have=%v", refHash, c.Hash.String())
	}
	if ctree, _ := c.Tree(); ctree != nil {
		f := templates.LookupFile(fileName, ctree)
		if f != nil {
			blob, err := templates.ReadBlob(f)
			if err != nil {
				return nil, err
			}
			if len(blob) > maxTemplateSize {
				return nil, fmt.Errorf("max template size [%v KB] reached for %v", maxTemplateSize/1000, f.Name)
			}
			t, err := templates.Parse(string(blob))
			if err != nil {
				return nil, err
			}
			parsedTemplate := bytes.NewBuffer([]byte{})
			if err := t.Execute(parsedTemplate, parameters); err != nil {
				return nil, err
			}
			return &openapi.Runbook{
				Name:       f.Name,
				InputFile:  parsedTemplate.Bytes(),
				EnvVars:    t.EnvVars(),
				CommitHash: c.Hash.String()}, nil
		}
	}
	return nil, fmt.Errorf("runbook %v not found for %v", fileName, c.Hash.String())
}

func listRunbookFiles(pluginConnectionList []*types.PluginConnection, config *templates.RunbookConfig) (*openapi.RunbookList, error) {
	commit, err := templates.FetchRepo(config)
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
		if !templates.IsRunbookFile(f.Name) {
			return nil
		}
		var connectionList []string
		for _, conn := range pluginConnectionList {
			if len(conn.Config) == 0 {
				connectionList = append(connectionList, conn.Name)
				continue
			}
			pathPrefix := conn.Config[0]
			if pathPrefix != "" && strings.HasPrefix(f.Name, pathPrefix) {
				connectionList = append(connectionList, conn.Name)
			}
		}

		runbook := &openapi.Runbook{
			Name:           f.Name,
			Metadata:       map[string]any{},
			ConnectionList: connectionList,
			Error:          nil,
		}
		blobData, err := templates.ReadBlob(f)
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
		t, err := templates.Parse(string(blobData))
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

func listRunbookFilesByPathPrefix(pathPrefix string, config *templates.RunbookConfig) (*openapi.RunbookList, error) {
	commit, err := templates.FetchRepo(config)
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
		if !templates.IsRunbookFile(f.Name) {
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
		blobData, err := templates.ReadBlob(f)
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
		t, err := templates.Parse(string(blobData))
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
