package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoophq/hoop/common/clientconfig"
)

const sample = "" +
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

func TestConfigHelpListsProfileCommands(t *testing.T) {
	path := writeConfig(t, sample)
	out := captureStdout(t, func() {
		code, err := dispatch([]string{"config"}, "/nonexistent/hoop", path)
		if err != nil || code != 0 {
			t.Fatalf("code=%d err=%v", code, err)
		}
	})
	for _, name := range []string{"clear", "create", "view", "get-contexts", "current-context", "use-context", "set-context", "delete-context"} {
		if !strings.Contains(out, name) {
			t.Fatalf("help missing %s:\n%s", name, out)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sample {
		t.Fatal("config help changed the file")
	}
}

func TestUseContextHelpDoesNotSwitch(t *testing.T) {
	path := writeConfig(t, sample)
	out := captureStdout(t, func() {
		code, err := dispatch([]string{"config", "use-context", "--help"}, "/nonexistent/hoop", path)
		if err != nil || code != 0 {
			t.Fatalf("code=%d err=%v", code, err)
		}
	})
	if !strings.Contains(out, "use-context <name>") {
		t.Fatalf("help = %s", out)
	}
	doc := readConfig(t, path)
	if doc.Current != "" {
		t.Fatalf("current = %s", doc.Current)
	}
}

func TestUseContextDoesNotCallTheRelease(t *testing.T) {
	path := writeConfig(t, sample)
	code, err := dispatch([]string{"config", "use-context", "profile2"}, "/nonexistent/hoop", path)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	doc := readConfig(t, path)
	if doc.Current != "profile2" || doc.Profiles[0].Token != "profile1-token" {
		t.Fatalf("doc current=%s profile1=%s", doc.Current, doc.Profiles[0].Token)
	}
}

func TestDelegateRestoresTheFileWhenTheReleaseDoesNotWrite(t *testing.T) {
	path := writeConfig(t, sample)
	fake := writeFake(t, "exit 0\n")
	code, err := delegate(fake, path, []string{"version"})
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sample {
		t.Fatalf("config changed:\n%s", got)
	}
	if _, err := os.Stat(backupPath(path)); !os.IsNotExist(err) {
		t.Fatal("backup remains")
	}
}

func TestDelegateMergesANewToken(t *testing.T) {
	path := writeConfig(t, sample)
	t.Setenv("HOOP_TEST_CONFIG", path)
	fake := writeFake(t, ""+
		"cat > \"$HOOP_TEST_CONFIG\" << 'EOF'\n"+
		"token = \"profile1-token-2\"\n"+
		"api_url = \"https://profile1.example:8009\"\n"+
		"grpc_url = \"grpcs://profile1.example:8010\"\n"+
		"tls_ca = \"Y+/=ca\"\n"+
		"skip_tls_verify = false\n"+
		"EOF\n")
	code, err := delegate(fake, path, nil)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	doc := readConfig(t, path)
	active, err := doc.Active()
	if err != nil {
		t.Fatal(err)
	}
	if active.Name != "profile1" || active.Token != "profile1-token-2" || active.TLSCA != "Y+/=ca" {
		t.Fatalf("active = %+v", active)
	}
	if doc.Profiles[1].Token != "profile2-token" || doc.Profiles[1].TLSCA != "aW1w" {
		t.Fatalf("profile2 changed: %+v", doc.Profiles[1])
	}
	if _, err := os.Stat(backupPath(path)); !os.IsNotExist(err) {
		t.Fatal("backup remains")
	}
}

func TestDelegateKeepsADeletedConfigDeleted(t *testing.T) {
	path := writeConfig(t, sample)
	t.Setenv("HOOP_TEST_CONFIG", path)
	fake := writeFake(t, "rm -f \"$HOOP_TEST_CONFIG\"\nexit 0\n")
	code, err := delegate(fake, path, nil)
	if err != nil || code != 0 {
		t.Fatalf("code=%d err=%v", code, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("config was restored after delete")
	}
	if _, err := os.Stat(backupPath(path)); !os.IsNotExist(err) {
		t.Fatal("backup remains")
	}
}

func TestDelegateReturnsTheChildExitCode(t *testing.T) {
	path := writeConfig(t, sample)
	fake := writeFake(t, "exit 4\n")
	code, err := delegate(fake, path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if code != 4 {
		t.Fatalf("code = %d", code)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sample {
		t.Fatalf("config changed:\n%s", got)
	}
}

func TestRecoverMergesFlatLeftByACrash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(backupPath(path), []byte(sample), 0600); err != nil {
		t.Fatal(err)
	}
	flat := clientconfig.Profile{
		Token:   "profile1-token-2",
		APIURL:  "https://profile1.example:8009",
		GrpcURL: "grpcs://profile1.example:8010",
		TLSCA:   "Y+/=ca",
	}
	if err := os.WriteFile(path, clientconfig.MarshalFlat(flat), 0600); err != nil {
		t.Fatal(err)
	}
	if err := recoverBackup(path); err != nil {
		t.Fatal(err)
	}
	doc := readConfig(t, path)
	active, err := doc.Active()
	if err != nil {
		t.Fatal(err)
	}
	if active.Token != "profile1-token-2" || doc.Profiles[1].Token != "profile2-token" {
		t.Fatalf("doc = %+v", doc.Profiles)
	}
	if _, err := os.Stat(backupPath(path)); !os.IsNotExist(err) {
		t.Fatal("backup remains")
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	_ = w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFake(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fakehoop")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func readConfig(t *testing.T, path string) clientconfig.ProfileFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := clientconfig.ParseProfileFile(data)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}
