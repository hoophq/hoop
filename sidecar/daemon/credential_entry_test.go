package daemon

import "testing"

// The vault block and its entries are checked on the plane, where the
// source is not linked: each mistake below is refused by the config alone.
func TestVaultConfigAndEntryValidation(t *testing.T) {
	for _, bad := range []CredentialEntry{
		{Name: "x", Source: SourceVault},
		{Name: "x", Source: SourceVault, Path: "/v1/secret/data/appdb"},
		{Name: "x", Source: SourceVault, Path: "database/creds/r", User: "u"},
	} {
		if err := ValidateVaultEntry(bad); err == nil {
			t.Errorf("entry %+v was accepted", bad)
		}
	}
	if err := ValidateVaultEntry(CredentialEntry{Path: "secret/data/appdb", UserKey: "username"}); err != nil {
		t.Errorf("a KV entry was refused: %v", err)
	}
	for _, cfg := range []VaultSourceConfig{
		{},
		{Addr: "http://vault"},
		{Addr: "https://vault", Auth: VaultAuthConfig{Type: "token"}},
		{Addr: "https://vault", Auth: VaultAuthConfig{Type: VaultAuthKubernetes}},
		{Addr: "https://vault", Auth: VaultAuthConfig{Type: VaultAuthTokenFile}},
	} {
		if p := cfg.Validate(); len(p) == 0 {
			t.Errorf("config %+v was accepted", cfg)
		}
	}
	good := VaultSourceConfig{Addr: "https://vault", Auth: VaultAuthConfig{Type: VaultAuthKubernetes, Role: "r"}}
	if p := good.Validate(); len(p) != 0 {
		t.Errorf("a kubernetes block was refused: %v", p)
	}
}
