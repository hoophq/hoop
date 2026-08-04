//go:build !windows

package proxy

import (
	"os/exec"
	"syscall"
)

// setMCPChildProcGroup puts the MCP server in its own process group so the
// whole tree can be signalled. An `npx`-style command execs node underneath
// itself; signalling only the direct child leaves the real server running and
// holding the connection's credentials.
func setMCPChildProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: 0}
}

// termProcessGroup asks the group led by pid to exit. Negating the pid is what
// makes the signal reach every descendant.
//
// SIGTERM is catchable, so this is a request, not a guarantee: a server with a
// broken handler, or one wedged in an uninterruptible read, ignores it. That is
// what killProcessGroup is for.
func termProcessGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGTERM) }

// killProcessGroup ends the group led by pid outright. SIGKILL cannot be caught
// or blocked, so this is the escalation that bounds cleanup.
func killProcessGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGKILL) }
