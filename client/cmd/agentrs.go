package cmd

import (
	"context"
	"fmt"
	agentconfig "github.com/hoophq/hoop/agent/config"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

var (
	defaultUserAgent = fmt.Sprintf("hoopagent/%v", version.Get().Version)
	vi               = version.Get()
)

func checkHoopKeyExists() bool {
	_, err := agentconfig.Load()
	if err != nil {
		log.With("version", vi.Version).Fatal(err)
		return false
	}
	return true
}

// for temporary start we will run hoop_rs if it exists in PATH or at a specified location
// and if hoop_rs exist we will try to run if fails we will run the go agent anyway
func RunAgentrs() {
	if !checkHoopKeyExists() {
		// hoop key not found, skipping hoop_rs startup
		log.Info("Hoop key not found, skipping hoop_rs startup")
		return
	}

	// inside the bundle builded by the ci the app/bin/hoop_rs is placed alonside hoop binary
	// and the PATH is set to app/bin so we can just look for hoop_rs in PATH
	defaultPath := "hoop_rs"
	// if the user has set HOOP_RS_BIN_PATH we will use that instead this is useful for development
	// and testing and make run-dev
	path := os.Getenv("HOOP_RS_BIN_PATH")
	fmt.Printf("Checking hoop_rs binary at path: %s\n", path)
	binaryPath := defaultPath

	if path != "" {
		binaryPath = path
	}

	log.Infof("Starting hoop_rs from: %s", binaryPath)

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binaryPath)

	// Set environment variables for the Rust agent
	cmd.Env = os.Environ()

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	// Set process group to ensure child processes are terminated
	setPgid(cmd)

	if err := cmd.Start(); err != nil {
		log.Errorf("Failed to start hoop_rs: %v", err)
		cancel()
		return
	}

	log.Infof("hoop_rs started with PID: %d", cmd.Process.Pid)

	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		log.Errorf("hoop_rs exited immediately with status: %v", cmd.ProcessState)
		cancel()
		return
	}

	// Set up signal handling to terminate hoop_rs when main process receives signals
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Infof("Received signal, terminating hoop_rs...")
		cancel() // This will terminate the hoop_rs process

		// Force kill if graceful termination doesn't work
		if cmd.Process != nil {
			killPid(cmd.Process.Pid)
		}
	}()

	// Run in background but monitor for completion
	go func() {
		if err := cmd.Wait(); err != nil {
			log.Errorf("hoop_rs exited with error: %v", err)
		} else {
			log.Infof("hoop_rs exited successfully")
		}
		cancel() // Cancel context when process exits
	}()

}
