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

// termProcessGroup has no graceful equivalent here: Windows offers no
// SIGTERM, and the console-control events that come closest do not reach a
// child spawned without its own console. So the escalation ladder collapses to
// one rung and this is a no-op — the stdin EOF before it is the polite step,
// and killProcessGroup is the enforcement.
func termProcessGroup(int) {}

// killProcessGroup terminates the child itself. Any grandchildren it spawned
// survive, which is the platform's behaviour rather than a choice.
func killProcessGroup(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
