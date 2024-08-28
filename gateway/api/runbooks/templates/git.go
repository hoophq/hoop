package templates

import (
	"fmt"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

func FetchRepo(rbConfig *RunbookConfig) (*object.Commit, error) {
	r, err := git.Init(memory.NewStorage(), memfs.New())
	if err != nil {
		return nil, err
	}

	_, err = r.CreateRemote(&config.RemoteConfig{
		Name: "origin",
		URLs: []string{rbConfig.GitURL},
	})
	if err != nil {
		return nil, fmt.Errorf("failed creating remote, err=%v", err)
	}
	err = r.Fetch(&git.FetchOptions{
		RemoteURL:  rbConfig.GitURL,
		Auth:       rbConfig.Auth,
		RemoteName: "origin",
		Tags:       git.NoTags,
		Depth:      1,
		// RefSpecs:   []config.RefSpec{"refs/heads/main:refs/heads/main"},
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return nil, fmt.Errorf("failed pulling repo %v, err=%v", rbConfig.GitURL, err)
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
