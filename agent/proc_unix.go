//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
)

// killPid sends a SIGTERM to the process group with the given pid.
func killPid(pid int) error { return syscall.Kill(-pid, syscall.SIGTERM) }
func setPgid(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}
}
