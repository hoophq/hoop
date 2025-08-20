package cmd

import (
	"fmt"
	"runtime"

	agentconfig "github.com/hoophq/hoop/agent/config"
	"github.com/hoophq/hoop/client/cmd/systemd"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/spf13/cobra"
)

var exampleAgentStartCmd = `
hoop agent install 
`
var exampleRemoveCmd = `
hoop remove systemd agent
`

var (
	startByOS = map[string]func(*cobra.Command) error{
		"linux": startLinuxAgent,
	}

	removeByOS = map[string]func(*cobra.Command) error{
		"linux": removeLinuxAgent,
	}

	stopByOS = map[string]func(*cobra.Command) error{
		"linux": stopLinuxAgent,
	}

	logsByOS = map[string]func(*cobra.Command) error{
		"linux": logsLinuxAgent,
	}
)

func Start(cmd *cobra.Command) error {
	if fn, ok := startByOS[runtime.GOOS]; ok {
		return fn(cmd)
	}
	return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}

func Stop(cmd *cobra.Command) error {
	if fn, ok := stopByOS[runtime.GOOS]; ok {
		return fn(cmd)
	}
	return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}

func Remove(cmd *cobra.Command) error {
	if fn, ok := removeByOS[runtime.GOOS]; ok {
		return fn(cmd)
	}
	return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}

func Logs(cmd *cobra.Command) error {
	if fn, ok := logsByOS[runtime.GOOS]; ok {
		return fn(cmd)
	}
	return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
}

var (
	agentCmd = &cobra.Command{
		Use:     "agent [COMMAND ..]",
		Example: exampleAgentStartCmd,
		Short:   "Install Hoop as a service/agent",
	}

	startLinuxAgentCmd = &cobra.Command{
		Use:     "start",
		Example: exampleAgentStartCmd,
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
		Example: exampleRemoveCmd,
		Short:   "Remove Hoop agent from service daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			return Remove(cmd)
		},
	}

	vi = version.Get()
)
func logsLinuxAgent(cmd *cobra.Command) error {
	systemd.Logs("hoop-agent")
	return nil
}

func startLinuxAgent(cmd *cobra.Command) error {
	cfg, err := agentconfig.Load()

	if err != nil {
		log.With("version", vi.Version).Fatal(err)
	}

	opts := systemd.Options{
		ServiceName: "hoop-agent",
		ExecArgs:    " start agent",
		Env: map[string]string{
			"HOOP_KEY": cfg.Token,
		},
		WantedBy: "default.target",
	}

	if err := systemd.Install(opts); err != nil {
		return err
	}

	cmd.Printf("✓ Installed and started %s\n", opts.ServiceName)
	return nil
}

func stopLinuxAgent(cmd *cobra.Command) error {
	if err := systemd.Stop("hoop-agent"); err != nil {
		return fmt.Errorf("failed to stop hoop-agent: %w", err)
	}
	cmd.Printf("✓ Stopped hoop-agent service\n")
	return nil
}

func removeLinuxAgent(cmd *cobra.Command) error {
	if err := systemd.Remove("hoop-agent"); err != nil {
		return err
	}
	cmd.Printf("✓ Removed and stopped hoop-agent\n")
	return nil
}

func init() {
	rootCmd.AddCommand(agentCmd)
	agentCmd.AddCommand(startLinuxAgentCmd)
	agentCmd.AddCommand(removeLinuxAgentCmd)
	agentCmd.AddCommand(stopLinuxAgentCmd)
	agentCmd.AddCommand(logsLinuxAgentCmd)

}
