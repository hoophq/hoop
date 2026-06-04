// Package versions implements `hoop versions <subcommand>`: a nvm-style
// manager for hoop CLI binaries installed under $HOME/.hoop/versions/.
package versions

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/hoophq/hoop/client/upgrade"
	"github.com/spf13/cobra"
)

// MainCmd is the parent `hoop versions` command.
var MainCmd = &cobra.Command{
	Use:   "versions",
	Short: "Manage installed hoop CLI versions",
	Long: `Manage hoop CLI binaries installed under $HOME/.hoop/versions/.

The currently active version is exposed as $HOME/.hoop/bin/hoop. Add
that directory to your PATH to use the active version from your shell.`,
	SilenceUsage: true,
}

var (
	installUseFlag       bool
	installReinstallFlag bool
	removeForceFlag      bool
)

func init() {
	installCmd.Flags().BoolVar(&installUseFlag, "use", false, "Switch the active version to the newly installed one")
	installCmd.Flags().BoolVar(&installReinstallFlag, "reinstall", false, "Force re-download even if the version is already installed")
	removeCmd.Flags().BoolVar(&removeForceFlag, "force", false, "Remove the version even if it is currently active")

	// `sync` and `upgrade` both download + activate a version, so they
	// share the post-install PATH-hint flags.
	for _, cmd := range []*cobra.Command{syncCmd, upgradeCmd} {
		cmd.Flags().BoolVarP(&yesFlag, "yes", "y", false, "Assume yes to interactive prompts (PATH setup)")
		cmd.Flags().BoolVar(&skipPathHint, "skip-path-hint", false, "Do not prompt to add $HOME/.hoop/bin to PATH")
	}

	MainCmd.AddCommand(listCmd, useCmd, installCmd, removeCmd, syncCmd, upgradeCmd)
}

var listCmd = &cobra.Command{
	Use:          "list",
	Aliases:      []string{"ls"},
	Short:        "List installed hoop versions",
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		layout, err := upgrade.DefaultLayout()
		if err != nil {
			return err
		}
		store, err := upgrade.LoadStore(layout)
		if err != nil {
			return err
		}
		entries := store.Sorted()
		if len(entries) == 0 {
			fmt.Println("No versions installed yet. Try `hoop versions sync`, `hoop versions upgrade`, or `hoop versions install <version>`.")
			return nil
		}
		headers := []string{" ", "VERSION", "PLATFORM", "INSTALLED AT", "PATH"}
		rows := make([][]string, 0, len(entries))
		for _, e := range entries {
			active := ""
			if e.Version == store.Active {
				active = "*"
			}
			rows = append(rows, []string{
				active,
				e.Version,
				e.Platform,
				e.InstalledAt.Local().Format(time.RFC3339),
				layout.VersionBinary(e.Version),
			})
		}
		fmt.Println(styles.RenderTable(headers, rows))
		if store.Active == "" {
			fmt.Println(styles.Fainted("(no active version set; run `hoop versions use <version>`)"))
		}
		return nil
	},
}

var useCmd = &cobra.Command{
	Use:          "use <version>",
	Short:        "Set the active hoop version",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, args []string) error {
		target := upgrade.NormalizeVersion(args[0])
		layout, err := upgrade.DefaultLayout()
		if err != nil {
			return err
		}
		store, err := upgrade.LoadStore(layout)
		if err != nil {
			return err
		}
		if !store.Has(target) {
			return fmt.Errorf("version %s is not installed; install it with `hoop versions install %s`", target, target)
		}
		if err := upgrade.SetActive(layout, store, target); err != nil {
			return err
		}
		if err := store.Save(layout); err != nil {
			return err
		}
		fmt.Printf("Active hoop version is now %s\n", target)
		fmt.Printf("Active CLI: %s\n", layout.BinLink)
		return nil
	},
}

var installCmd = &cobra.Command{
	Use:          "install <version>",
	Short:        "Download and install a specific hoop version",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, args []string) error {
		target := upgrade.NormalizeVersion(args[0])
		if target == "" {
			return errors.New("a non-empty version is required")
		}
		platform, err := upgrade.CurrentPlatform()
		if err != nil {
			return err
		}
		layout, err := upgrade.DefaultLayout()
		if err != nil {
			return err
		}
		store, err := upgrade.LoadStore(layout)
		if err != nil {
			return err
		}

		if installReinstallFlag {
			if _, ok := store.Get(target); ok {
				if err := os.RemoveAll(layout.VersionDir(target)); err != nil {
					return fmt.Errorf("failed removing old install dir: %w", err)
				}
				store.Remove(target)
			}
		} else if existing, ok := store.Get(target); ok {
			if _, statErr := os.Stat(layout.VersionBinary(target)); statErr == nil {
				fmt.Printf("Version %s already installed at %s (use --reinstall to overwrite).\n", existing.Version, layout.VersionBinary(target))
				if installUseFlag {
					if err := upgrade.SetActive(layout, store, target); err != nil {
						return err
					}
					if err := store.Save(layout); err != nil {
						return err
					}
					fmt.Printf("Active hoop version is now %s\n", target)
				}
				return nil
			}
		}

		fmt.Printf("Installing %s for %s ...\n", target, platform)
		installer := upgrade.NewInstaller(layout)
		entry, err := installer.Install(target, platform, store)
		if err != nil {
			return err
		}
		fmt.Printf("Installed %s at %s\n", entry.Version, layout.VersionBinary(entry.Version))

		if installUseFlag {
			if err := upgrade.SetActive(layout, store, target); err != nil {
				return err
			}
			if err := store.Save(layout); err != nil {
				return err
			}
			fmt.Printf("Active hoop version is now %s\n", target)
		} else {
			fmt.Printf("Run `hoop versions use %s` to make it the active version.\n", target)
		}
		return nil
	},
}

var removeCmd = &cobra.Command{
	Use:          "remove <version>",
	Aliases:      []string{"rm", "uninstall"},
	Short:        "Remove an installed hoop version",
	Args:         cobra.ExactArgs(1),
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, args []string) error {
		target := upgrade.NormalizeVersion(args[0])
		layout, err := upgrade.DefaultLayout()
		if err != nil {
			return err
		}
		store, err := upgrade.LoadStore(layout)
		if err != nil {
			return err
		}
		if !store.Has(target) {
			return fmt.Errorf("version %s is not installed", target)
		}
		if store.Active == target && !removeForceFlag {
			return fmt.Errorf("version %s is currently active; pass --force to remove it (this will remove %s)", target, layout.BinLink)
		}

		if err := os.RemoveAll(layout.VersionDir(target)); err != nil {
			return fmt.Errorf("failed removing %s: %w", layout.VersionDir(target), err)
		}
		store.Remove(target)
		if store.Active == "" {
			// Removing the active version cleared store.Active; also drop
			// the bin path (symlink on Unix, copy on Windows) so we don't
			// leave a dangling link or a stale copy behind.
			if _, err := os.Lstat(layout.BinLink); err == nil {
				if err := os.Remove(layout.BinLink); err != nil {
					return fmt.Errorf("failed removing active CLI path %s: %w", layout.BinLink, err)
				}
			}
		}
		if err := store.Save(layout); err != nil {
			return err
		}
		fmt.Printf("Removed %s\n", target)
		return nil
	},
}
