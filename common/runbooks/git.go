package runbooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

const maxTemplateSize = 1000000 // 1MB

type File struct {
	Name      string
	EnvVars   map[string]string
	InputFile []byte
	CommitSHA string
}

type Repository struct {
	files map[string]*object.File
}

var ErrNotFound = errors.New("runbook file not found")

// FetchRepository fetches the git repository and returns a map of files
func FetchRepository(config *Config) (*Repository, error) {
	commit, err := CloneRepositoryInMemory(config)
	if err != nil {
		return nil, err
	}
	if commit.Hash.IsZero() {
		return nil, fmt.Errorf("commit hash from remote is empty")
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed obtaining tree from commit %v, %v", commit.Hash.String(), err)
	}

	files := map[string]*object.File{}
	err = tree.Files().ForEach(func(f *object.File) error {
		// replace with the commit sha of the repository
		f.Hash = commit.Hash
		files[f.Name] = f
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed iterating through tree, reason=%v", err)
	}

	return &Repository{files: files}, nil
}

func (r *Repository) ReadFile(fileName string, parameters map[string]string) (*File, error) {
	f, ok := r.files[fileName]
	if !ok || f == nil {
		return nil, nil
	}
	if f.Size > maxTemplateSize {
		return nil, fmt.Errorf("max template size [%v KB] reached for %v", maxTemplateSize/1000, f.Name)
	}
	reader, err := f.Blob.Reader()
	if err != nil {
		return nil, fmt.Errorf("reader error %v", err)
	}
	blob, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed reading file %v, %v", fileName, err)
	}
	t, err := Parse(string(blob))
	if err != nil {
		return nil, err
	}
	parsedTemplate := bytes.NewBuffer([]byte{})
	if err := t.Execute(parsedTemplate, parameters); err != nil {
		return nil, err
	}
	return &File{
		Name:      f.Name,
		InputFile: parsedTemplate.Bytes(),
		EnvVars:   t.EnvVars(),
		CommitSHA: f.Hash.String(),
	}, nil
}

func CloneRepositoryInMemory(runbookConf *Config) (*object.Commit, error) {
	if err := runbookConf.loadKnownHosts(); err != nil {
		return nil, err
	}
	r, err := git.Init(memory.NewStorage(), memfs.New())
	if err != nil {
		return nil, err
	}

	_, err = r.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{runbookConf.GitURL},
	})
	if err != nil {
		return nil, fmt.Errorf("failed creating remote, err=%v", err)
	}
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*30)
	defer cancelFn()
	err = r.FetchContext(ctx, &git.FetchOptions{
		RemoteURL:  runbookConf.GitURL,
		Auth:       runbookConf.Auth,
		RemoteName: "origin",
		Tags:       git.NoTags,
		Depth:      1,
		// RefSpecs:   []config.RefSpec{"refs/heads/main:refs/heads/main"},
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return nil, fmt.Errorf("failed pulling repo %v, err=%v", runbookConf.GitURL, err)
	}
	refs, err := r.References()
	if err != nil {
		return nil, fmt.Errorf("failed getting references, err=%v", err)
	}
	var refList []string
	var resRef *plumbing.Reference
	refs.ForEach(func(ref *plumbing.Reference) error {
		if resRef != nil {
			return nil
		}
		// The HEAD is omitted in a `git show-ref` so we ignore the symbolic
		// references, the HEAD
		if ref.Type() == plumbing.SymbolicReference {
			return nil
		}
		if ref.Name() == "refs/remotes/origin/master" || ref.Name() == "refs/remotes/origin/main" {
			resRef = ref
			return nil
		}
		refList = append(refList, fmt.Sprintf("%v=%s", ref.Name(), ref.Hash().String()))
		return nil
	})
	if resRef != nil {
		return r.CommitObject(resRef.Hash())
	}
	return nil, fmt.Errorf("master or main ref not found. refs=%v", refList)
}
