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

// killProcessGroup signals the group led by pid. Negating the pid is what
// makes the signal reach every descendant.
func killProcessGroup(pid int) { _ = syscall.Kill(-pid, syscall.SIGTERM) }
