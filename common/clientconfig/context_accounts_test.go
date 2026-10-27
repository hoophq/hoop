package clientconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSpacedProfileHeaders(t *testing.T) {
	const raw = `
[profile profile1]
token = "profile1-token"
api_url = "https://profile1.example:8009"
grpc_url = "grpcs://profile1.example:8010"
tls_ca = "Y+/=ca"
skip_tls_verify = false

[profile profile2]
token = "profile2-token"
api_url = "https://profile2.example:8009"
grpc_url = "grpcs://profile2.example:8010"
tls_ca = "aW1w"
skip_tls_verify = false
`
	doc, err := ParseProfileFile([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	active, err := doc.Active()
	if err != nil {
		t.Fatal(err)
	}
	if active.Name != "profile1" || active.Token != "profile1-token" || active.TLSCA != "Y+/=ca" {
		t.Fatalf("active = %+v", active)
	}
	if doc.Profiles[1].Name != "profile2" || doc.Profiles[1].APIURL != "https://profile2.example:8009" {
		t.Fatalf("second = %+v", doc.Profiles[1])
	}
}

func TestUseProfileKeepsBothAccountsInOneFile(t *testing.T) {
	path := writeSample(t)
	selected, err := UseProfile(path, "profile2")
	if err != nil {
		t.Fatal(err)
	}
	if selected.APIURL != "https://profile2.example:8009" {
		t.Fatalf("api = %s", selected.APIURL)
	}
	doc := readSample(t, path)
	active, err := doc.Active()
	if err != nil {
		t.Fatal(err)
	}
	if active.Name != "profile2" || active.Token != "profile2-token" {
		t.Fatalf("active = %+v", active)
	}
	if doc.Profiles[0].Name != "profile1" || doc.Profiles[0].Token != "profile1-token" || doc.Profiles[0].TLSCA != "Y+/=ca" {
		t.Fatalf("profile1 changed: %+v", doc.Profiles[0])
	}
	if _, err := UseProfile(path, "missing"); err == nil {
		t.Fatal("expected an error")
	}
	doc = readSample(t, path)
	if doc.Current != "profile2" {
		t.Fatalf("current changed after a failed switch: %s", doc.Current)
	}
}

func TestUpdateActiveProfileLeavesTheOtherProfile(t *testing.T) {
	path := writeSample(t)
	if _, err := UseProfile(path, "profile1"); err != nil {
		t.Fatal(err)
	}
	err := UpdateActiveProfile(path, Profile{
		Token:   "profile1-token-2",
		APIURL:  "https://profile1.example:8009",
		GrpcURL: "grpcs://profile1.example:8010",
		TLSCA:   "Y+/=ca",
	})
	if err != nil {
		t.Fatal(err)
	}
	doc := readSample(t, path)
	if doc.Profiles[0].Token != "profile1-token-2" {
		t.Fatalf("profile1 token = %s", doc.Profiles[0].Token)
	}
	if doc.Profiles[1].Token != "profile2-token" || doc.Profiles[1].APIURL != "https://profile2.example:8009" {
		t.Fatalf("profile2 changed: %+v", doc.Profiles[1])
	}
}

func TestDottedHeaderAndDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "" +
		"[profile.profile1]\n" +
		"token = \"profile1-token\"\n" +
		"api_url = \"https://profile1.example:8009\"\n" +
		"grpc_url = \"grpcs://profile1.example:8010\"\n" +
		"skip_tls_verify = false\n" +
		"\n" +
		"[profile.profile2]\n" +
		"token = \"profile2-token\"\n" +
		"api_url = \"https://profile2.example:8009\"\n" +
		"grpc_url = \"grpcs://profile2.example:8010\"\n" +
		"skip_tls_verify = true\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	list, err := ListProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !list[0].Current || list[1].SkipTLSVerify != true {
		t.Fatalf("list = %+v", list)
	}
	if err := DeleteProfile(path, "profile1"); err == nil {
		t.Fatal("deleted the active profile")
	}
	if err := DeleteProfile(path, "profile2"); err != nil {
		t.Fatal(err)
	}
	list, err = ListProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "profile1" {
		t.Fatalf("list = %+v", list)
	}
}

func TestSaveProfileConvertsAFlatFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "token = \"only-token\"\napi_url = \"https://only.example\"\ngrpc_url = \"grpcs://only.example:8010\"\nskip_tls_verify = false\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	doc, err := ParseProfileFile([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if doc.HasProfiles() {
		t.Fatal("flat file was treated as profiles")
	}
	saved, err := SaveProfile(path, "only")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Name != "only" || saved.Token != "only-token" {
		t.Fatalf("saved = %+v", saved)
	}
	if _, err := SaveProfile(path, "../x"); err == nil {
		t.Fatal("accepted an unsafe name")
	}
}

func TestMarshalFlatRoundTrip(t *testing.T) {
	in := Profile{
		Token:         `tok"en`,
		APIURL:        "https://profile1.example:8009",
		GrpcURL:       "grpcs://profile1.example:8010",
		TLSCA:         "Y+/=ca",
		SkipTLSVerify: false,
	}
	doc, err := ParseProfileFile(MarshalFlat(in))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := doc.Flat()
	if !ok {
		t.Fatal("flat file was treated as profiles")
	}
	if got.Token != in.Token || got.APIURL != in.APIURL || got.GrpcURL != in.GrpcURL || got.TLSCA != in.TLSCA || got.SkipTLSVerify {
		t.Fatalf("flat = %+v", got)
	}
}

func TestHomeProfileFileParsesWhenPresent(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	data, err := os.ReadFile(filepath.Join(home, ".hoop", "config.toml"))
	if err != nil {
		t.Skip(err)
	}
	if !strings.Contains(string(data), "[profile") {
		t.Skip("flat config")
	}
	doc, err := ParseProfileFile(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Profiles) < 2 {
		t.Fatalf("profiles = %d", len(doc.Profiles))
	}
	for _, p := range doc.Profiles {
		if p.APIURL == "" || p.Token == "" {
			t.Fatalf("profile %s is missing credentials", p.Name)
		}
	}
}

func writeSample(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	body := "" +
		"[profile profile1]\n" +
		"token = \"profile1-token\"\n" +
		"api_url = \"https://profile1.example:8009\"\n" +
		"grpc_url = \"grpcs://profile1.example:8010\"\n" +
		"tls_ca = \"Y+/=ca\"\n" +
		"skip_tls_verify = false\n" +
		"\n" +
		"[profile profile2]\n" +
		"token = \"profile2-token\"\n" +
		"api_url = \"https://profile2.example:8009\"\n" +
		"grpc_url = \"grpcs://profile2.example:8010\"\n" +
		"tls_ca = \"aW1w\"\n" +
		"skip_tls_verify = false\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readSample(t *testing.T, path string) ProfileFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseProfileFile(data)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
