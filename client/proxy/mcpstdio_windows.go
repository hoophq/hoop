//go:build windows

package proxy

import (
	"os"
	"os/exec"
)

// Windows has no POSIX process groups, so there is nothing to configure at
// spawn time. The polite shutdown that runs first — closing the child's stdin
// — is what a well-behaved stdio MCP server actually responds to.
func setMCPChildProcGroup(*exec.Cmd) {}

// killProcessGroup kills the child itself. Any grandchildren it spawned
// survive, which is the platform's behaviour rather than a choice; the stdin
// EOF that precedes this is the reliable path.
func killProcessGroup(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
