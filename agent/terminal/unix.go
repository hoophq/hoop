//go:build !windows

package terminal

import (
	"os/exec"
	"syscall"
)

// killPid sends a SIGINT to the process group with the given pid.
func killPid(pid int) error { return syscall.Kill(-pid, syscall.SIGINT) }
func setPgid(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    0,
	}
}
