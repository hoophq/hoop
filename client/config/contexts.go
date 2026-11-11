package clientconfig

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/gofrs/flock"
	"github.com/hoophq/hoop/common/clientconfig"
)

// ErrContextNotFound is returned when a named context has no file.
var ErrContextNotFound = errors.New("context not found")

// ErrUnsavedConfig is returned by Use when config.toml holds an installation
// that no context keeps: switching would lose it.
var ErrUnsavedConfig = errors.New("the current configuration is not saved in a context")

const (
	contextsDirName  = "contexts"
	activeContextKey = "current"
	lockFileName     = ".lock"
	// lockTimeout covers another `hoop context use` that waits on its gateway
	lockTimeout = 30 * time.Second
)

// Lower case only: macOS and Windows file systems ignore case, so Dev and dev
// would share one file.
var contextNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Context is one named installation. Its keys are the keys of config.toml,
// so a context file and config.toml stay interchangeable.
type Context struct {
	Name          string `toml:"-"`
	Token         string `toml:"token"`
	ApiURL        string `toml:"api_url"`
	GrpcURL       string `toml:"grpc_url"`
	TlsCAB64Enc   string `toml:"tls_ca"`
	SkipTLSVerify bool   `toml:"skip_tls_verify"`
}

// Config returns a client config for this context. It is not bound to a file.
func (c *Context) Config() *Config {
	return &Config{
		Token:         c.Token,
		ApiURL:        c.ApiURL,
		GrpcURL:       c.GrpcURL,
		TlsCAB64Enc:   c.TlsCAB64Enc,
		SkipTLSVerify: c.SkipTLSVerify,
		InsecureGRPC:  hasInsecureScheme(c.GrpcURL),
	}
}

// ContextStore keeps contexts in <home>/contexts/<name>.toml and the active name
// in <home>/contexts/current. Older CLIs read only config.toml, which Use writes.
type ContextStore struct {
	dir        string
	configFile string
}

// NewContextStore returns a store rooted at hoopHomeDir (normally $HOME/.hoop).
func NewContextStore(hoopHomeDir string) *ContextStore {
	return &ContextStore{
		dir:        filepath.Join(hoopHomeDir, contextsDirName),
		configFile: filepath.Join(hoopHomeDir, clientconfig.ClientFile),
	}
}

// DefaultContextStore returns the store under $HOME/.hoop.
func DefaultContextStore() (*ContextStore, error) {
	_, hoopHomeDir, err := clientconfig.NewPath(clientconfig.ClientFile)
	if err != nil {
		return nil, err
	}
	return NewContextStore(hoopHomeDir), nil
}

// ValidateContextName refuses names that are not a plain file name.
func ValidateContextName(name string) error {
	if !contextNameRe.MatchString(name) || name == activeContextKey {
		return fmt.Errorf("invalid context name %q: use 1-64 lower-case letters, digits, '.', '_' or '-', starting with a letter or digit (%q is reserved)",
			name, activeContextKey)
	}
	return nil
}

func (s *ContextStore) path(name string) string {
	return filepath.Join(s.dir, name+".toml")
}

// Get reads one context.
func (s *ContextStore) Get(name string) (*Context, error) {
	if err := ValidateContextName(name); err != nil {
		return nil, err
	}
	var ctx Context
	if _, err := toml.DecodeFile(s.path(name), &ctx); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrContextNotFound, name)
		}
		return nil, fmt.Errorf("failed decoding context %s: %v", name, err)
	}
	ctx.Name = name
	return &ctx, nil
}

// Exists reports whether a context file exists.
func (s *ContextStore) Exists(name string) bool {
	_, err := os.Stat(s.path(name))
	return err == nil
}

// Save writes a context file with mode 0600.
func (s *ContextStore) Save(ctx *Context) error {
	if err := ValidateContextName(ctx.Name); err != nil {
		return err
	}
	if ctx.ApiURL == "" {
		return fmt.Errorf("context %s has no api_url", ctx.Name)
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return fmt.Errorf("failed creating %s: %v", s.dir, err)
	}
	return writeTOML(s.path(ctx.Name), ctx)
}

// List returns every context, sorted by name.
func (s *ContextStore) List() ([]*Context, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".toml")
		if e.IsDir() || !ok || ValidateContextName(name) != nil {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]*Context, 0, len(names))
	for _, name := range names {
		ctx, err := s.Get(name)
		if err != nil {
			return nil, err
		}
		items = append(items, ctx)
	}
	return items, nil
}

// Remove deletes a context. When it is the active one, the active marker
// goes too; config.toml is left as it is.
func (s *ContextStore) Remove(name string) error {
	if err := ValidateContextName(name); err != nil {
		return err
	}
	if err := os.Remove(s.path(name)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrContextNotFound, name)
		}
		return err
	}
	active, err := s.activeName()
	if err != nil {
		return fmt.Errorf("context %s removed, but reading the active marker failed: %v", name, err)
	}
	if active == name {
		if err := os.Remove(filepath.Join(s.dir, activeContextKey)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *ContextStore) activeName() (string, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, activeContextKey))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// Active returns the active context, or "" when there is none or config.toml
// points at another api_url (`hoop config create` can do that).
func (s *ContextStore) Active() (string, error) {
	current, err := s.activeConfig()
	if err != nil || current == nil {
		return "", err
	}
	return current.Name, nil
}

// activeConfig reads config.toml once and returns it, named after the active
// context, when it still points at that context's api_url. Otherwise nil.
func (s *ContextStore) activeConfig() (*Context, error) {
	name, err := s.activeName()
	if err != nil || name == "" {
		return nil, err
	}
	ctx, err := s.Get(name)
	if errors.Is(err, ErrContextNotFound) {
		// the marker names a context that was removed by hand
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	current, err := s.ReadConfigFile()
	if err != nil || current.ApiURL != ctx.ApiURL {
		return nil, err
	}
	current.Name = name
	return current, nil
}

// Resolve returns a context as in use: config.toml for the active one (it holds
// a login made since the last switch), the context file for any other.
func (s *ContextStore) Resolve(name string) (*Context, error) {
	ctx, err := s.Get(name)
	if err != nil {
		return nil, err
	}
	current, err := s.activeConfig()
	if err != nil {
		return nil, err
	}
	if current != nil && current.Name == name {
		return current, nil
	}
	return ctx, nil
}

// ReadConfigFile reads config.toml as stored, without the HOOP_* env overrides
// that Load applies. A missing file reads as an empty context.
func (s *ContextStore) ReadConfigFile() (*Context, error) {
	var ctx Context
	if _, err := toml.DecodeFile(s.configFile, &ctx); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed decoding %s: %v", s.configFile, err)
	}
	return &ctx, nil
}

// SetActive marks name as the active context. It does not touch config.toml.
func (s *ContextStore) SetActive(name string) error {
	if _, err := s.Get(name); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(s.dir, activeContextKey), []byte(name+"\n")); err != nil {
		return fmt.Errorf("failed marking context %s active: %v", name, err)
	}
	return nil
}

// Use saves config.toml back into the active context, then writes name into
// config.toml and marks it active. Without force, it refuses a config no context keeps.
func (s *ContextStore) Use(name string, force bool) (*Context, error) {
	target, err := s.Get(name)
	if err != nil {
		return nil, err
	}
	if !force {
		if err := s.checkSaved(); err != nil {
			return nil, err
		}
	}
	if err := s.SaveBack(); err != nil {
		return nil, err
	}
	// a save-back into the target itself changes what we must write
	if target, err = s.Get(name); err != nil {
		return nil, err
	}
	// atomic, so an interrupted switch leaves the old config.toml, never a broken one
	if err := writeTOML(s.configFile, target.Config()); err != nil {
		return nil, err
	}
	if err := s.SetActive(name); err != nil {
		return nil, err
	}
	return target, nil
}

// checkSaved returns ErrUnsavedConfig when config.toml is not the active context
// and no context holds its api_url and token.
func (s *ContextStore) checkSaved() error {
	current, err := s.ReadConfigFile()
	if err != nil || current.ApiURL == "" {
		return err
	}
	if active, err := s.Active(); err != nil || active != "" {
		return err
	}
	items, err := s.List()
	if err != nil {
		return err
	}
	for _, ctx := range items {
		if ctx.ApiURL == current.ApiURL && ctx.Token == current.Token {
			return nil
		}
	}
	return fmt.Errorf("%w (api_url=%s)", ErrUnsavedConfig, current.ApiURL)
}

// SaveBack copies config.toml into the active context, so a login made since
// the last switch is kept. It does nothing when no context is active.
func (s *ContextStore) SaveBack() error {
	// save the same read of config.toml that matched the active api_url
	current, err := s.activeConfig()
	if err != nil || current == nil {
		return err
	}
	return s.Save(current)
}

// Lock takes the store's lock file, so two hoop processes cannot interleave
// their writes to config.toml and the contexts. Call unlock when done.
func (s *ContextStore) Lock() (unlock func(), err error) {
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return nil, fmt.Errorf("failed creating %s: %v", s.dir, err)
	}
	fl := flock.New(filepath.Join(s.dir, lockFileName))
	ctx, cancel := context.WithTimeout(context.Background(), lockTimeout)
	defer cancel()
	if ok, err := fl.TryLockContext(ctx, 50*time.Millisecond); !ok {
		return nil, fmt.Errorf("another hoop context command holds %s: %v", fl.Path(), err)
	}
	return func() { _ = fl.Unlock() }, nil
}

func writeTOML(path string, v any) error {
	buf := bytes.NewBuffer(nil)
	if err := toml.NewEncoder(buf).Encode(v); err != nil {
		return fmt.Errorf("failed encoding %s: %v", path, err)
	}
	return writeFileAtomic(path, buf.Bytes())
}

// writeFileAtomic replaces path in one rename, so an interrupted write never
// leaves a truncated file behind.
func writeFileAtomic(path string, data []byte) error {
	// a unique temp file, so two writers never rename each other's file; mode 0600
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("failed writing %s: %v", path, err)
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return fmt.Errorf("failed writing %s: %v", path, err)
	}
	return nil
}
