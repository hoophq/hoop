package daemon

import (
	"os"
	"os/exec"
)

type runner interface {
	Run(name string, args ...string) (string, error)
	Logs(name string, args ...string) error
}

type Runner struct{}

func (Runner) Run(name string, args ...string) (string, error) { return run(name, args...) }
func (Runner) Logs(name string, args ...string) error { return logs(name, args...) }

var execRunner runner = Runner{}

func logs(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Stdin = os.Stdin
	return cmd.Run()

}
func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
