package runbooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

const maxTemplateSize = 1_000_000 // 1MB

type File struct {
	Name               string
	EnvVars            map[string]string
	TemplateAttributes map[string]any
	InputFile          []byte
	CommitSHA          string
}

type Repository struct {
	files map[string]*object.File
}

var ErrNotFound = errors.New("runbook file not found")
var ErrFileAlreadyExists = errors.New("file already exists in repository, use overwrite=true to replace it")

type CommitFileInput struct {
	// Path of the file relative to the repository root, e.g. "ops/restart.runbook.sh"
	Path string
	// Content of the file
	Content string
	// CommitMessage defaults to "feat: add <path>" when empty
	CommitMessage string
	// AuthorName is the git commit author name
	AuthorName string
	// AuthorEmail is the git commit author email
	AuthorEmail string
	// Overwrite controls whether an existing file should be replaced
	Overwrite bool
}

// CommitAndPushFile clones the repository in memory, writes the file, commits with the given author, and pushes.
// Returns the commit SHA on success.
func CommitAndPushFile(runbookConf *Config, input *CommitFileInput) (string, error) {
	if err := runbookConf.loadKnownHosts(); err != nil {
		return "", err
	}

	branches := []string{"main", "master"}
	if runbookConf.Branch != "" {
		branches = []string{runbookConf.Branch}
	}

	var r *git.Repository
	var clonedBranch string
	var lastErr error
	for _, branch := range branches {
		var err error
		r, err = git.Clone(memory.NewStorage(), memfs.New(), &git.CloneOptions{
			URL:           runbookConf.GitURL,
			Auth:          runbookConf.Auth,
			ReferenceName: plumbing.NewBranchReferenceName(branch),
			SingleBranch:  true,
			Depth:         1,
		})
		if err == nil {
			clonedBranch = branch
			break
		}
		lastErr = err
	}
	if r == nil {
		return "", fmt.Errorf("failed cloning repository: %v", lastErr)
	}

	w, err := r.Worktree()
	if err != nil {
		return "", fmt.Errorf("failed getting worktree: %v", err)
	}

	_, statErr := w.Filesystem.Stat(input.Path)
	if statErr == nil && !input.Overwrite {
		return "", ErrFileAlreadyExists
	}

	if dir := path.Dir(input.Path); dir != "." {
		if err := w.Filesystem.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("failed creating directory structure: %v", err)
		}
	}

	f, err := w.Filesystem.Create(input.Path)
	if err != nil {
		return "", fmt.Errorf("failed creating file: %v", err)
	}
	if _, err := f.Write([]byte(input.Content)); err != nil {
		f.Close()
		return "", fmt.Errorf("failed writing file: %v", err)
	}
	f.Close()

	if _, err := w.Add(input.Path); err != nil {
		return "", fmt.Errorf("failed staging file: %v", err)
	}

	commitMsg := input.CommitMessage
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("feat: add %s", input.Path)
	}

	hash, err := w.Commit(commitMsg, &git.CommitOptions{
		Author: &object.Signature{
			Name:  input.AuthorName,
			Email: input.AuthorEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed creating commit: %v", err)
	}

	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*30)
	defer cancelFn()

	refSpec := config.RefSpec(fmt.Sprintf("refs/heads/%s:refs/heads/%s", clonedBranch, clonedBranch))
	err = r.PushContext(ctx, &git.PushOptions{
		Auth:       runbookConf.Auth,
		RemoteName: "origin",
		RefSpecs:   []config.RefSpec{refSpec},
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return "", fmt.Errorf("failed pushing to repository: %v", err)
	}

	return hash.String(), nil
}

// FetchRepository fetches the git repository and returns a map of files
func FetchRepository(config *Config) (*Repository, error) {
	commit, err := CloneRepositoryInMemory(config)
	if err != nil {
		return nil, err
	}

	return BuildRepositoryFromCommit(commit)
}

func BuildRepositoryFromCommit(commit *object.Commit) (*Repository, error) {
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
		Name:               f.Name,
		InputFile:          parsedTemplate.Bytes(),
		TemplateAttributes: t.Attributes(),
		EnvVars:            t.EnvVars(),
		CommitSHA:          f.Hash.String(),
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

	remoteName := "origin"
	err = r.FetchContext(ctx, &git.FetchOptions{
		RemoteURL:  runbookConf.GitURL,
		Auth:       runbookConf.Auth,
		RemoteName: remoteName,
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
	defer refs.Close()

	var branches []string
	if runbookConf.Branch == "" {
		// If no specific branch is set, use the default branches
		branches = []string{"master", "main"}
	} else {
		branches = []string{runbookConf.Branch}
	}

	var refList []string
	var resRef *plumbing.Reference
	for {
		ref, err := refs.Next()
		if err != nil {
			break
		}

		// The HEAD is omitted in a `git show-ref` so we ignore the symbolic
		// references, the HEAD
		if ref.Type() == plumbing.SymbolicReference {
			continue
		}

		matchesBranch := slices.ContainsFunc(branches, func(branch string) bool {
			return ref.Name().Short() == fmt.Sprintf("%s/%s", remoteName, branch)
		})
		if matchesBranch {
			resRef = ref
			break
		}

		refList = append(refList, fmt.Sprintf("%v=%s", ref.Name(), ref.Hash().String()))
	}

	if resRef != nil {
		return r.CommitObject(resRef.Hash())
	}

	return nil, fmt.Errorf("branch ref not found. branches=%v refs=%v", branches, refList)
}
