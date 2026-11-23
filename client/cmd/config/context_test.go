package config

import "testing"

func TestContextCommandsAreRegistered(t *testing.T) {
	names := map[string]bool{}
	for _, cmd := range MainCmd.Commands() {
		names[cmd.Name()] = true
	}
	for _, name := range []string{
		"get-contexts",
		"current-context",
		"use-context",
		"set-context",
		"delete-context",
	} {
		if !names[name] {
			t.Errorf("missing command %s", name)
		}
	}
	use, _, err := MainCmd.Find([]string{"use"})
	if err != nil {
		t.Fatal(err)
	}
	if use.Name() != "use-context" {
		t.Fatalf("use resolved to %s", use.Name())
	}
}
