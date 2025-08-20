package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	agentconfig "github.com/hoophq/hoop/agent/config"
	"github.com/hoophq/hoop/client/cmd/systemd"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/spf13/cobra"
)

var exampleInstallCmd = `
hoop install systemd agent
`
var exampleRemoveCmd = `
hoop remove systemd agent
`

var (
	installCmd = &cobra.Command{
		Use:     "install [COMMAND ..]",
		Example: exampleInstallCmd,
		Short:   "Install Hoop as a service/agent",
	}

	installSystemdCmd = &cobra.Command{
		Use:     "systemd agent",
		Example: exampleInstallCmd,
		Short:   "Install Hoop as a service/agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			return installSystemd(cmd)
		},
	}

	removeCmd = &cobra.Command{
		Use:     "remove [COMMAND ..]",
		Example: exampleRemoveCmd,
		Short:   "Remove Hoop service/agent",
	}

	removeSystemCmd = &cobra.Command{
		Use:     "systemd agent",
		Example: exampleRemoveCmd,
		Short:   "Remove Hoop systemd service",
		RunE: func(cmd *cobra.Command, args []string) error {
			return removeSystemd(cmd)
		},
	}

	vi = version.Get()
)

func removeSystemd(cmd *cobra.Command) error {
	if runtime.GOOS != "linux" {
		return errors.New("systemd removal is only supported on Linux")
	}
	if err := systemd.Remove("hoop-agent", true); err != nil {
		return err
	}
	cmd.Printf("✓ Removed and stopped hoop-agent\n")
	return nil
}

func installSystemd(cmd *cobra.Command) error {
	cfg, err := agentconfig.Load()
	if err != nil {
		log.With("version", vi.Version).Fatal(err)
	}

	if runtime.GOOS != "linux" {
		log.Fatal("Sysmted agent installation is only supported on Linux")
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve executable symlinks: %w", err)
	}

	opts := systemd.Options{
		ServiceName: "hoop-agent",
		ExecPath:    exe,
		ExecArgs:    " start agent",
		Env: map[string]string{
			"HOOP_KEY": cfg.Token,
		},
		UserMode: true,
		WantedBy: "multi-user.target",
	}

	if err := systemd.Install(opts); err != nil {
		return err
	}

	cmd.Printf("✓ Installed and started %s\n", opts.ServiceName)
	return nil
}

func run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func init() {
	rootCmd.AddCommand(installCmd)
	installCmd.AddCommand(installSystemdCmd)

	rootCmd.AddCommand(removeCmd)
	removeCmd.AddCommand(removeSystemCmd)

}
