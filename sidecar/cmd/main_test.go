package main

import (
	"testing"

	"github.com/hoophq/hoop/sidecar/daemon"
)

// hoop-inspect links the mcp lane engine; a build without it refuses an mcp
// lane at validate.
func TestHoopInspectLinksTheMCPEngine(t *testing.T) {
	cfg, err := daemon.LoadConfigBytes([]byte(`{"listeners":[{"name":"m","protocol":"mcp","listen":"127.0.0.1:18901","upstream":"https://mcp.example.com/mcp"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := daemon.Validate(cfg, nil); err != nil {
		t.Fatal(err)
	}
}
