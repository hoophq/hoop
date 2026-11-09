package clientconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewPathHonorsHOOPConfig(t *testing.T) {
	target := filepath.Join(t.TempDir(), "profile2.toml")
	t.Setenv(ConfigFileEnv, "  "+target+"  ")

	got, _, err := NewPath(ClientFile)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Fatalf("got %s", got)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("override path was created")
	}
}

func TestNewPathHOOPConfigRelative(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv(ConfigFileEnv, "profile2.toml")

	got, _, err := NewPath(ClientFile)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dir, "profile2.toml") {
		t.Fatalf("got %s", got)
	}
}

func TestNewPathHOOPConfigTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(ConfigFileEnv, "~/profile2.toml")

	got, _, err := NewPath(ClientFile)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(home, "profile2.toml") {
		t.Fatalf("got %s", got)
	}
}

func TestNewPathOverrideSkipsMissingHome(t *testing.T) {
	target := filepath.Join(t.TempDir(), "client.toml")
	t.Setenv(ConfigFileEnv, target)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "missing-home"))

	got, _, err := NewPath(ClientFile)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Fatalf("got %s", got)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".hoop")); !os.IsNotExist(err) {
		t.Fatal("override created a home directory")
	}
}

func TestNewPathHOOPConfigRejectsBareTilde(t *testing.T) {
	t.Setenv(ConfigFileEnv, "~")
	_, _, err := NewPath(ClientFile)
	if err == nil || !strings.Contains(err.Error(), "HOOP_CONFIG") {
		t.Fatalf("err = %v", err)
	}
}
