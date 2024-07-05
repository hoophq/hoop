package agent

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/hoophq/hoop/agent/controller"
	"github.com/hoophq/hoop/common/log"
)

func newCommand(envs map[string]string, args []string) *exec.Cmd {
	if len(args) == 0 {
		return nil
	}
	mainCmd := args[0]
	if len(args) > 1 {
		args = args[1:]
	}
	cmd := exec.Command(mainCmd, args...)
	cmd.Env = []string{}
	for key, val := range envs {
		_, key, _ = strings.Cut(key, ":")
		if key == "" {
			continue
		}
		val, _ := base64.StdEncoding.DecodeString(val)
		if val == nil {
			continue
		}
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, val))
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// it configures the command to start in a new process group
	// it will allow killing child processes when the parent process is killed
	setPgid(cmd)

	// c.cmd.Env = envStore.ParseToKeyVal()
	// c.cmd.Env = append(c.cmd.Env, fmt.Sprintf("PATH=%v", os.Getenv("PATH")))
	return cmd
}

func killProcess(cmd *exec.Cmd) error {
	if cmd != nil && cmd.Process != nil {
		proc := cmd.Process
		log.Debugf("sending SIGTERM to process %v", proc.Pid)
		if e := killPid(proc.Pid); e != nil {
			return fmt.Errorf("failed sending SIGTERM to process %v, reason=%v", proc.Pid, e)
		}
		var isRunning bool
		for i := 0; i < 10; i++ {
			isRunning = proc.Signal(syscall.Signal(0)) == nil
			if !isRunning {
				break
			}
			if i == 5 {
				log.Debugf("still waiting process to exit ...")
			}
			time.Sleep(time.Second)
		}
		if isRunning {
			log.Debugf("sending SIGKILL to process %v", proc.Pid)
			if e := proc.Kill(); e != nil {
				return fmt.Errorf("failed sending SIGKILL to process %v, reason=%v", proc.Pid, e)
			}
		}
	}
	return nil
}

func cleanupAgentInstance(shutdownFn context.CancelFunc, err error) (cleanExit bool) {
	log.Infof("cleaning agent instance, reason=%v", err)
	obj := agentStore.Pop(agentInstanceKey)
	instance, _ := obj.(*controller.Agent)
	timeoutCtx, timeoutCancelFn := context.WithTimeout(context.Background(), time.Second*10)
	go func() {
		if instance != nil {
			instance.Close(err)
		}
		if shutdownFn != nil {
			shutdownFn()
		}
		timeoutCancelFn()
	}()
	<-timeoutCtx.Done()
	if err := timeoutCtx.Err(); err == context.DeadlineExceeded {
		log.Warnf("timeout (10s) waiting for agent to close graceful")
		return
	}
	return true
}

func handleOsInterrupt(shutdownFn context.CancelFunc) {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		sigval := <-sigc
		err := fmt.Errorf("received signal '%v' from the operating system", sigval)
		log.Debug(err)
		cleanExit := cleanupAgentInstance(shutdownFn, err)
		sentry.Flush(time.Second * 2)
		log.With("clean-exit", cleanExit).Debugf("exiting program")
		switch sigval {
		case syscall.SIGHUP:
			os.Exit(int(syscall.SIGHUP))
		case syscall.SIGINT:
			os.Exit(int(syscall.SIGINT))
		case syscall.SIGTERM:
			os.Exit(int(syscall.SIGTERM))
		case syscall.SIGQUIT:
			os.Exit(int(syscall.SIGQUIT))
		}
	}()
}
