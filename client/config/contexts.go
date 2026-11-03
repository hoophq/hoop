package clientconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/hoophq/hoop/common/clientconfig"
)

// ErrContextNotFound is returned when a named context has no file.
var ErrContextNotFound = errors.New("context not found")

const (
	contextsDirName  = "contexts"
	activeContextKey = "current"
)

var contextNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

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

// ContextStore keeps contexts in <home>/contexts/<name>.toml and the name of
// the active one in <home>/contexts/current. Older CLIs read only
// <home>/config.toml, which Use writes in the format they know.
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
		return fmt.Errorf("invalid context name %q: use 1-64 letters, digits, '.', '_' or '-', starting with a letter or digit (%q is reserved)",
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
	if active, _ := s.activeName(); active == name {
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

// Active returns the name of the active context, or "" when there is none.
// A context counts as active only while config.toml still points at its
// api_url: `hoop config create` can point config.toml somewhere else.
func (s *ContextStore) Active() (string, error) {
	name, err := s.activeName()
	if err != nil || name == "" {
		return "", err
	}
	ctx, err := s.Get(name)
	if err != nil {
		return "", nil
	}
	current, err := s.ReadConfigFile()
	if err != nil || current.ApiURL != ctx.ApiURL {
		return "", nil
	}
	return name, nil
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

// Use makes name the active context. First it saves config.toml back into
// the active context, so a token from `hoop login` is kept. Then it writes the
// target into config.toml and marks it active.
func (s *ContextStore) Use(name string) (*Context, error) {
	target, err := s.Get(name)
	if err != nil {
		return nil, err
	}
	if err := s.saveBack(); err != nil {
		return nil, err
	}
	// a save-back into the target itself changes what we must write
	if target, err = s.Get(name); err != nil {
		return nil, err
	}
	conf := target.Config()
	conf.filepath = s.configFile
	if _, err := conf.Save(); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(s.dir, activeContextKey), []byte(name+"\n"), 0600); err != nil {
		return nil, fmt.Errorf("failed marking context %s active: %v", name, err)
	}
	return target, nil
}

func (s *ContextStore) saveBack() error {
	active, err := s.Active()
	if err != nil || active == "" {
		return err
	}
	current, err := s.ReadConfigFile()
	if err != nil {
		return err
	}
	current.Name = active
	return s.Save(current)
}

func writeTOML(path string, v any) error {
	buf := bytes.NewBuffer(nil)
	if err := toml.NewEncoder(buf).Encode(v); err != nil {
		return fmt.Errorf("failed encoding %s: %v", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0600); err != nil {
		return fmt.Errorf("failed writing %s: %v", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("failed writing %s: %v", path, err)
	}
	return nil
}
