package agent

import "os/exec"

// killPid sends a SIGTERM to the process group with the given pid.
func killPid(pid int) error { return nil }
func setPgid(cmd *exec.Cmd) { return }
