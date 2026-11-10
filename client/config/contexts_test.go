package clientconfig

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"
)

// legacyConfig is the struct an older CLI decodes config.toml into.
type legacyConfig struct {
	Token         string `toml:"token"`
	ApiURL        string `toml:"api_url"`
	GrpcURL       string `toml:"grpc_url"`
	TlsCAB64Enc   string `toml:"tls_ca"`
	SkipTLSVerify bool   `toml:"skip_tls_verify"`
}

func readLegacy(t *testing.T, path string) legacyConfig {
	t.Helper()
	var c legacyConfig
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		t.Fatalf("config.toml has keys an older CLI does not know: %v", undecoded)
	}
	return c
}

func TestValidateContextName(t *testing.T) {
	for _, ok := range []string{"dev", "hoop-prd", "br.stg_1", "a"} {
		if err := ValidateContextName(ok); err != nil {
			t.Errorf("%q: unexpected error %v", ok, err)
		}
	}
	// upper case: Dev and dev are one file on macOS and Windows
	for _, bad := range []string{"", "../x", "a/b", ".hidden", "-x", "current", "a b", "Dev", "A"} {
		if err := ValidateContextName(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestContextUseWritesLegacyConfigAndKeepsLogin(t *testing.T) {
	home := t.TempDir()
	s := NewContextStore(home)
	cfg := filepath.Join(home, "config.toml")

	must(t, s.Save(&Context{Name: "dev", ApiURL: "https://dev.example.com", GrpcURL: "grpcs://dev.example.com:8443", Token: "dev-token"}))
	must(t, s.Save(&Context{Name: "prd", ApiURL: "https://prd.example.com"}))

	if _, err := s.Use("dev", false); err != nil {
		t.Fatal(err)
	}
	got := readLegacy(t, cfg)
	if got.ApiURL != "https://dev.example.com" || got.Token != "dev-token" || got.GrpcURL != "grpcs://dev.example.com:8443" {
		t.Fatalf("config.toml after use dev = %+v", got)
	}
	if active, _ := s.Active(); active != "dev" {
		t.Fatalf("active = %q, want dev", active)
	}

	// prd has no login yet: switch, then `hoop login` writes config.toml.
	if _, err := s.Use("prd", false); err != nil {
		t.Fatal(err)
	}
	if got := readLegacy(t, cfg); got.ApiURL != "https://prd.example.com" || got.Token != "" {
		t.Fatalf("config.toml after use prd = %+v", got)
	}
	login := &Config{filepath: cfg, ApiURL: "https://prd.example.com", Token: "prd-token"}
	if _, err := login.Save(); err != nil {
		t.Fatal(err)
	}

	// switching away saves the new login into prd
	if _, err := s.Use("dev", false); err != nil {
		t.Fatal(err)
	}
	prd, err := s.Get("prd")
	must(t, err)
	if prd.Token != "prd-token" {
		t.Fatalf("prd token = %q, want the login saved back", prd.Token)
	}
	if got := readLegacy(t, cfg); got.Token != "dev-token" {
		t.Fatalf("config.toml token = %q, want dev-token", got.Token)
	}
}

func TestContextSaveBackSkipsForeignConfig(t *testing.T) {
	home := t.TempDir()
	s := NewContextStore(home)
	cfg := filepath.Join(home, "config.toml")
	must(t, s.Save(&Context{Name: "dev", ApiURL: "https://dev.example.com", Token: "dev-token"}))
	must(t, s.Save(&Context{Name: "prd", ApiURL: "https://prd.example.com", Token: "prd-token"}))
	if _, err := s.Use("dev", false); err != nil {
		t.Fatal(err)
	}
	// `hoop config create` points config.toml at another installation
	other := &Config{filepath: cfg, ApiURL: "https://other.example.com", Token: "other-token"}
	if _, err := other.Save(); err != nil {
		t.Fatal(err)
	}
	if active, _ := s.Active(); active != "" {
		t.Fatalf("active = %q, want none once config.toml points elsewhere", active)
	}
	// that login is in no context: refuse, and leave config.toml alone
	if _, err := s.Use("prd", false); !errors.Is(err, ErrUnsavedConfig) {
		t.Fatalf("use prd over an unsaved config: err = %v, want ErrUnsavedConfig", err)
	}
	if got := readLegacy(t, cfg); got.ApiURL != "https://other.example.com" || got.Token != "other-token" {
		t.Fatalf("config.toml changed after a refused use: %+v", got)
	}
	// --force overwrites it, and still saves nothing into dev
	if _, err := s.Use("prd", true); err != nil {
		t.Fatal(err)
	}
	dev, err := s.Get("dev")
	must(t, err)
	if dev.ApiURL != "https://dev.example.com" || dev.Token != "dev-token" {
		t.Fatalf("dev was overwritten with a foreign config: %+v", dev)
	}
}

func TestContextUseAcceptsConfigSavedInAContext(t *testing.T) {
	home := t.TempDir()
	s := NewContextStore(home)
	cfg := filepath.Join(home, "config.toml")
	// a CLI configured before contexts existed
	old := &Config{filepath: cfg, ApiURL: "https://dev.example.com", Token: "dev-token"}
	if _, err := old.Save(); err != nil {
		t.Fatal(err)
	}
	must(t, s.Save(&Context{Name: "prd", ApiURL: "https://prd.example.com"}))
	if _, err := s.Use("prd", false); !errors.Is(err, ErrUnsavedConfig) {
		t.Fatalf("err = %v, want ErrUnsavedConfig", err)
	}
	// `hoop context create dev` copies config.toml and marks it active
	must(t, s.Save(&Context{Name: "dev", ApiURL: "https://dev.example.com", Token: "dev-token"}))
	must(t, s.SetActive("dev"))
	if active, _ := s.Active(); active != "dev" {
		t.Fatalf("active = %q, want dev", active)
	}
	if _, err := s.Use("prd", false); err != nil {
		t.Fatalf("use prd once config.toml is saved: %v", err)
	}
}

func TestContextResolveUsesConfigForActive(t *testing.T) {
	home := t.TempDir()
	s := NewContextStore(home)
	cfg := filepath.Join(home, "config.toml")
	must(t, s.Save(&Context{Name: "dev", ApiURL: "https://dev.example.com", Token: "old"}))
	must(t, s.Save(&Context{Name: "prd", ApiURL: "https://prd.example.com", Token: "prd"}))
	if _, err := s.Use("dev", false); err != nil {
		t.Fatal(err)
	}
	// `hoop login` refreshes the token in config.toml only
	login := &Config{filepath: cfg, ApiURL: "https://dev.example.com", Token: "new"}
	if _, err := login.Save(); err != nil {
		t.Fatal(err)
	}
	dev, err := s.Resolve("dev")
	must(t, err)
	if dev.Token != "new" || dev.Name != "dev" {
		t.Fatalf("resolve active = %+v, want the login in config.toml", dev)
	}
	prd, err := s.Resolve("prd")
	must(t, err)
	if prd.Token != "prd" {
		t.Fatalf("resolve inactive = %+v, want the context file", prd)
	}
}

func TestContextActiveReportsUnreadableConfig(t *testing.T) {
	home := t.TempDir()
	s := NewContextStore(home)
	must(t, s.Save(&Context{Name: "dev", ApiURL: "https://dev.example.com"}))
	if _, err := s.Use("dev", false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("not = [toml"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Active(); err == nil {
		t.Fatal("a broken config.toml read as no active context")
	}
	// a marker naming a removed context is not an error
	must(t, os.Remove(filepath.Join(home, "contexts", "dev.toml")))
	if active, err := s.Active(); err != nil || active != "" {
		t.Fatalf("active = %q, %v", active, err)
	}
}

func TestContextFilesArePrivate(t *testing.T) {
	home := t.TempDir()
	s := NewContextStore(home)
	must(t, s.Save(&Context{Name: "dev", ApiURL: "https://dev.example.com", Token: "secret"}))
	st, err := os.Stat(filepath.Join(home, "contexts", "dev.toml"))
	must(t, err)
	if mode := st.Mode().Perm(); mode != 0600 {
		t.Fatalf("mode = %v, want 0600", mode)
	}
}

func TestContextListAndRemove(t *testing.T) {
	home := t.TempDir()
	s := NewContextStore(home)
	if items, err := s.List(); err != nil || len(items) != 0 {
		t.Fatalf("empty store: %v, %v", items, err)
	}
	must(t, s.Save(&Context{Name: "prd", ApiURL: "https://prd.example.com"}))
	must(t, s.Save(&Context{Name: "dev", ApiURL: "https://dev.example.com"}))
	if _, err := s.Use("dev", false); err != nil {
		t.Fatal(err)
	}
	items, err := s.List()
	must(t, err)
	if len(items) != 2 || items[0].Name != "dev" || items[1].Name != "prd" {
		t.Fatalf("list = %+v", items)
	}
	must(t, s.Remove("dev"))
	if active, _ := s.Active(); active != "" {
		t.Fatalf("active = %q after removing it", active)
	}
	if err := s.Remove("dev"); !errors.Is(err, ErrContextNotFound) {
		t.Fatalf("remove twice: %v", err)
	}
	if _, err := s.Use("missing", false); !errors.Is(err, ErrContextNotFound) {
		t.Fatalf("use missing: %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Two hoop processes switching at once must not save one installation into the
// other's context. Each goroutine opens its own store, as a process does.
func TestContextUseConcurrentKeepsEachInstallation(t *testing.T) {
	home := t.TempDir()
	s := NewContextStore(home)
	want := map[string]Context{
		"dev": {ApiURL: "https://dev.example.com", Token: "dev-token"},
		"prd": {ApiURL: "https://prd.example.com", Token: "prd-token"},
	}
	for name, c := range want {
		must(t, s.Save(&Context{Name: name, ApiURL: c.ApiURL, Token: c.Token}))
	}
	if _, err := s.Use("dev", false); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := range 4 {
		name := []string{"dev", "prd"}[i%2]
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := NewContextStore(home)
			for range 50 {
				unlock, err := s.Lock()
				if err != nil {
					errs <- err
					return
				}
				_, err = s.Use(name, false)
				unlock()
				if err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	for name, c := range want {
		got, err := s.Get(name)
		must(t, err)
		if got.ApiURL != c.ApiURL || got.Token != c.Token {
			t.Fatalf("%s = %+v, want %+v", name, got, c)
		}
	}
}

func TestContextUseLeavesNoTempFiles(t *testing.T) {
	home := t.TempDir()
	s := NewContextStore(home)
	must(t, s.Save(&Context{Name: "dev", ApiURL: "https://dev.example.com", Token: "t"}))
	if _, err := s.Use("dev", false); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{home, filepath.Join(home, "contexts")} {
		entries, err := os.ReadDir(dir)
		must(t, err)
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".tmp" {
				t.Fatalf("temp file left behind: %s", filepath.Join(dir, e.Name()))
			}
		}
	}
	// config.toml stays private after an atomic replace
	st, err := os.Stat(filepath.Join(home, "config.toml"))
	must(t, err)
	if mode := st.Mode().Perm(); mode != 0600 {
		t.Fatalf("config.toml mode = %v, want 0600", mode)
	}
	if got := readLegacy(t, filepath.Join(home, "config.toml")); got.Token != "t" {
		t.Fatalf("config.toml = %+v", got)
	}
}
