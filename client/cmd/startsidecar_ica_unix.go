//go:build !windows

package cmd

import (
	"os"
	"syscall"
)

// execHoopInspect replaces this process with hoop-inspect: the PID, stdio
// and signal disposition carry over, so whatever started `hoop start
// sidecar` supervises hoop-inspect from here on. It returns only on failure.
func execHoopInspect(path string, argv []string) error {
	return syscall.Exec(path, argv, os.Environ())
}
