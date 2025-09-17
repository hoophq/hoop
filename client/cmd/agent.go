package cmd

import (
	"fmt"
	"runtime"

	"github.com/hoophq/hoop/client/cmd/daemon"
	"github.com/spf13/cobra"
)

var (
	startByOS = map[string]func() error{
		"linux":  daemon.StartLinuxAgent,
		"darwin": daemon.StartDarwinAgent,
	}

	removeByOS = map[string]func() error{
		"linux":  daemon.RemoveLinuxAgent,
		"darwin": daemon.RemoveDarwinAgent,
	}

	stopByOS = map[string]func() error{
		"linux":  daemon.StopLinuxAgent,
		"darwin": daemon.StopDarwinAgent,
	}

	logsByOS = map[string]func() error{
		"linux":  daemon.LogsLinuxAgent,
		"darwin": daemon.LogsDarwinAgent,
	}

	envFilesByOS = map[string]func() error{
		"linux":  daemon.EnvFilesAgent,
		"darwin": daemon.EnvFilesAgent,
	}
)

func Start(cmd *cobra.Command) error {
	if fn, ok := startByOS[runtime.GOOS]; ok {
		err := fn()
		if err != nil {
			return fmt.Errorf("failed to start agent: %w", err)
		}
		cmd.Printf("✓ Installed and started hoop-agent service\n")
		return nil
	}
	return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}

func Stop(cmd *cobra.Command) error {
	if fn, ok := stopByOS[runtime.GOOS]; ok {
		err := fn()
		if err != nil {
			return fmt.Errorf("failed to stop agent: %w", err)
		}
		cmd.Printf("✓ Stopped hoop-agent service\n")
		return nil
	}
	return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}

func Remove(cmd *cobra.Command) error {
	if fn, ok := removeByOS[runtime.GOOS]; ok {
		err := fn()
		if err != nil {
			return fmt.Errorf("failed to remove agent: %w", err)
		}
		cmd.Printf("✓ Removed and stopped hoop-agent\n")
		return nil
	}
	return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}

func Logs(cmd *cobra.Command) error {
	if fn, ok := logsByOS[runtime.GOOS]; ok {
		err := fn()
		if err != nil {
			return fmt.Errorf("failed to get agent logs: %w", err)
		}
		cmd.Printf("✓ Showing hoop-agent logs\n")
		return nil
	}
	return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}

func EnvFiles(cmd *cobra.Command) error {
	if fn, ok := envFilesByOS[runtime.GOOS]; ok {
		err := fn()
		if err != nil {
			return fmt.Errorf("failed to get agent env file: %w", err)
		}
		cmd.Printf("✓ Showing hoop-agent env file path\n")
		return nil
	}
	return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}

var (
	agentCmd = &cobra.Command{
		Use:     "agent [COMMAND ..]",
		Example: "hoop agent",
		Short:   "Install Hoop as a service/agent",
	}

	startLinuxAgentCmd = &cobra.Command{
		Use:     "start",
		Example: "hoop agent start",
		Short:   "Hoop agent start as a service daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return Start(cmd)
		},
	}

	stopLinuxAgentCmd = &cobra.Command{
		Use:     "stop",
		Example: "hoop agent stop",
		Short:   "Stop Hoop agent service daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return Stop(cmd)
		},
	}

	logsLinuxAgentCmd = &cobra.Command{
		Use:     "logs",
		Example: "hoop agent logs",
		Short:   "Show Hoop agent logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			return Logs(cmd)
		},
	}

	removeLinuxAgentCmd = &cobra.Command{
		Use:     "remove",
		Example: "hoop agent remove",
		Short:   "Remove Hoop agent from service daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return Remove(cmd)
		},
	}

	envFilesCmd = &cobra.Command{
		Use:     "env",
		Example: "hoop agent env",
		Short:   "Show Hoop agent environment file keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			return EnvFiles(cmd)
		},
	}
)

func init() {
	rootCmd.AddCommand(agentCmd)
	agentCmd.AddCommand(envFilesCmd)

	agentCmd.AddCommand(startLinuxAgentCmd)
	agentCmd.AddCommand(removeLinuxAgentCmd)
	agentCmd.AddCommand(stopLinuxAgentCmd)
	agentCmd.AddCommand(logsLinuxAgentCmd)
}
