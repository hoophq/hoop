package terminal

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"syscall"

	"github.com/runopsio/hoop/common/runtime"
	term "github.com/runopsio/hoop/common/terminal"

	"github.com/creack/pty"
)

type Command struct {
	cmd      *exec.Cmd
	envStore *EnvVarStore
	ptty     *os.File
}

type OnExecErrFn func(exitCode int, errMsg string, a ...any)

func (c *Command) Environ() []string {
	if c.cmd != nil {
		return c.cmd.Environ()
	}
	return nil
}

func (c *Command) String() string {
	if c.cmd != nil {
		return c.cmd.String()
	}
	return ""
}

func (c *Command) Pid() int {
	if c.cmd != nil && c.cmd.Process != nil {
		return c.cmd.Process.Pid
	}
	return -1
}

func (c *Command) Close() error {
	procPid := c.Pid()
	if procPid != -1 {
		log.Printf("sending SIGTERM signal to process %v ...", procPid)
		return runtime.Kill(procPid, syscall.SIGTERM)
	}
	if c.ptty != nil {
		return c.ptty.Close()
	}
	return nil
}

// OnPreExec execute all pre terminal env functions
func (c *Command) OnPreExec() error {
	for _, env := range c.envStore.store {
		if env.OnPreExec == nil {
			continue
		}
		if err := env.OnPreExec(); err != nil {
			return fmt.Errorf("failed storing environment variable %q, err=%v", env.Key, err)
		}
	}
	return nil
}

// OnPostExec execute all post terminal env functions
func (c *Command) OnPostExec() error {
	for _, env := range c.envStore.store {
		if env.OnPostExec == nil {
			continue
		}
		if err := env.OnPostExec(); err != nil {
			return fmt.Errorf("failed storing environment variable %q, err=%v", env.Key, err)
		}
	}
	return nil
}

func (c *Command) Run(stdoutw, stderrw io.WriteCloser, stdinInput []byte, onExecErr OnExecErrFn, clientArgs ...string) error {
	pipeStdout, err := c.cmd.StdoutPipe()
	if err != nil {
		onExecErr(term.InternalErrorExitCode, "internal error, failed returning stdout pipe")
		return err
	}
	pipeStderr, err := c.cmd.StderrPipe()
	if err != nil {
		onExecErr(term.InternalErrorExitCode, "internal error, failed returning stderr pipe")
		return err
	}
	if err := c.OnPreExec(); err != nil {
		onExecErr(term.InternalErrorExitCode, "internal error, failed executing pre command")
		return fmt.Errorf("failed executing pre command, err=%v", err)
	}
	var stdin bytes.Buffer
	c.cmd.Stdin = &stdin
	exitCode := term.InternalErrorExitCode
	if err := c.cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			// path not found error exit code
			exitCode = 127
		}
		onExecErr(exitCode, "failed starting command")
		return err
	}
	if _, err := stdin.Write(stdinInput); err != nil {
		onExecErr(term.InternalErrorExitCode, "internal error, failed writing input")
		return err
	}
	stdoutCh := copyBuffer(stdoutw, pipeStdout, 1024, "stdout")
	stderrCh := copyBuffer(stderrw, pipeStderr, 1024, "stderr")

	go func() {
		exitCode = 0
		// wait must be called after reading all contents from pipes (stdout,stderr)
		<-stdoutCh
		<-stderrCh
		err := c.cmd.Wait()
		if err != nil {
			if exErr, ok := err.(*exec.ExitError); ok {
				exitCode = exErr.ExitCode()
				if exitCode == -1 {
					exitCode = term.InternalErrorExitCode
				}
			}
		}
		if err := c.OnPostExec(); err != nil {
			fmt.Printf("failed executing post command, err=%v", err)
		}
		if exitCode == 0 {
			onExecErr(exitCode, "")
			return
		}
		onExecErr(exitCode, "failed executing command, err=%v", err)
	}()
	return nil
}

func (c *Command) RunOnTTY(stdoutWriter io.WriteCloser, onExecErr OnExecErrFn) error {
	// Start the command with a pty.
	if err := c.OnPreExec(); err != nil {
		return fmt.Errorf("failed executing pre execution command, err=%v", err)
	}
	c.cmd.Env = append(c.cmd.Env, "TERM=xterm-256color")

	ptmx, err := pty.Start(c.cmd)
	if err != nil {
		return fmt.Errorf("failed starting pty, err=%v", err)
	}
	c.ptty = ptmx
	go func() {
		// TODO: need to make distinction between stderr / stdout when writing back to client
		if _, err := io.Copy(stdoutWriter, ptmx); err != nil {
			log.Printf("failed copying stdout from tty, err=%v", err)
		}

		log.Println("closing tty ...")
		if err := ptmx.Close(); err != nil {
			log.Printf("failed closing tty, err=%v", err)
		}
		if err := c.OnPostExec(); err != nil {
			log.Printf("failed executing post execution command, err=%v", err)
		}

		exitCode := 0
		err := c.cmd.Wait()
		if err != nil {
			if exErr, ok := err.(*exec.ExitError); ok {
				exitCode = exErr.ExitCode()
				// assume that it was killed or interrupted
				// because the process is probably started already
				if exitCode == -1 {
					exitCode = 1
				}
			}
		}
		if exitCode == 0 {
			onExecErr(exitCode, "")
			return
		}
		onExecErr(exitCode, "failed executing command, err=%v", err)
	}()
	return nil
}

func (c *Command) WriteTTY(data []byte) error {
	if c.ptty == nil {
		return fmt.Errorf("tty is empty")
	}
	// this is required to avoid redacting inputs (e.g.: paste content)
	if len(data) >= 29 {
		for _, b := range data {
			if _, err := c.ptty.Write([]byte{b}); err != nil {
				return err
			}
		}
		return nil
	}
	_, err := c.ptty.Write(data)
	return err
}

func (c *Command) ResizeTTY(size *pty.Winsize) error {
	return pty.Setsize(c.ptty, size)
}

func NewCommand(rawEnvVarList map[string]interface{}, args ...string) (*Command, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("connection must be at least one argument")
	}
	envStore, err := NewEnvVarStore(rawEnvVarList)
	if err != nil {
		return nil, err
	}
	mainCmd := args[0]
	execArgs := args[1:]
	if len(execArgs) > 0 {
		var err error
		execArgs, err = expandEnvVarToCmd(envStore, execArgs)
		if err != nil {
			return nil, err
		}
	}
	c := &Command{envStore: envStore}
	c.cmd = exec.Command(mainCmd, execArgs...)
	c.cmd.Env = envStore.ParseToKeyVal()
	c.cmd.Env = append(c.cmd.Env, fmt.Sprintf("PATH=%v", os.Getenv("PATH")))
	return c, nil
}

func copyBuffer(dst io.Writer, src io.Reader, bufSize int, stream string) chan struct{} {
	doneCh := make(chan struct{})
	go func() {
		wb, err := io.CopyBuffer(dst, src, make([]byte, bufSize))
		switch err {
		case io.EOF: // noop
		case nil:
			log.Printf("[%s] - done copying, written=%v", stream, wb)
		default:
			log.Printf("[%s] - fail to copy, written=%v, err=%v", stream, wb, err)
		}
		close(doneCh)
	}()
	return doneCh
}
