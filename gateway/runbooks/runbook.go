package runbooks

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/runbooks/templates"
)

const maxTemplateSize = 50000 // 50KB

func fetchRunbookFile(config *templates.RunbookConfig, req RunbookRequest) (*Runbook, error) {
	c, err := templates.FetchRepo(config)
	if err != nil {
		return nil, err
	}
	if c.Hash.IsZero() {
		return nil, fmt.Errorf("commit hash from remote is empty")
	}
	if req.RefHash != "" && req.RefHash != c.Hash.String() {
		return nil, fmt.Errorf("mismatch git commit, want=%v, have=%v", req.RefHash, c.Hash.String())
	}
	if ctree, _ := c.Tree(); ctree != nil {
		f := templates.LookupFile(req.FileName, ctree)
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
			if err := t.Execute(parsedTemplate, req.Parameters); err != nil {
				return nil, err
			}
			return &Runbook{
				Name:       f.Name,
				InputFile:  parsedTemplate.Bytes(),
				EnvVars:    t.EnvVars(),
				CommitHash: c.Hash.String()}, nil
		}
	}
	return nil, fmt.Errorf("runbook %v not found for %v", req.FileName, c.Hash.String())
}

func listRunbookFiles(pluginConnectionList []plugin.Connection, config *templates.RunbookConfig) (*RunbookList, error) {
	commit, err := templates.FetchRepo(config)
	if err != nil {
		return nil, err
	}
	runbookList := &RunbookList{
		Commit:        commit.Hash.String(),
		CommitAuthor:  commit.Author.String(),
		CommitMessage: commit.Message,
		Items:         []*Runbook{},
	}
	ctree, _ := commit.Tree()
	if ctree == nil {
		return runbookList, nil
	}
	return runbookList, ctree.Files().ForEach(func(f *object.File) error {
		if !templates.IsRunbookFile(f.Name) {
			return nil
		}
		blobData, err := templates.ReadBlob(f)
		if err != nil {
			return fmt.Errorf("name=%v - read blob error %v", f.Name, err)
		}
		if len(blobData) > maxTemplateSize {
			return fmt.Errorf("max template size [%v KB] reached for %v", maxTemplateSize/1000, f.Name)
		}
		t, err := templates.Parse(string(blobData))
		if err != nil {
			return fmt.Errorf("name=%v - failed parsing template %v", f.Name, err)
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
		runbookList.Items = append(runbookList.Items, &Runbook{
			Name:           f.Name,
			Metadata:       t.Attributes(),
			ConnectionList: connectionList,
		})
		return nil
	})
}

func listRunbookFilesByPathPrefix(pathPrefix string, config *templates.RunbookConfig) (*RunbookList, error) {
	commit, err := templates.FetchRepo(config)
	if err != nil {
		return nil, err
	}
	runbookList := &RunbookList{
		Commit:        commit.Hash.String(),
		CommitAuthor:  commit.Author.String(),
		CommitMessage: commit.Message,
		Items:         []*Runbook{},
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
		blobData, err := templates.ReadBlob(f)
		if err != nil {
			return fmt.Errorf("name=%v - read blob error %v", f.Name, err)
		}
		if len(blobData) > maxTemplateSize {
			return fmt.Errorf("max template size [%v KB] reached for %v", maxTemplateSize/1000, f.Name)
		}
		t, err := templates.Parse(string(blobData))
		if err != nil {
			return fmt.Errorf("name=%v - failed parsing template %v", f.Name, err)
		}
		runbookList.Items = append(runbookList.Items, &Runbook{
			Name:           f.Name,
			Metadata:       t.Attributes(),
			ConnectionList: nil,
		})
		return nil
	})
}
