package runtime

import (
	"os"
	"syscall"
)

func Kill(pid int, signum syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(signum)
}
