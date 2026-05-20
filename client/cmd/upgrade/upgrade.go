// Package upgrade implements the `hoop upgrade` command: download and
// switch to the hoop CLI version that matches the connected gateway.
package upgrade

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/client/upgrade"
	"github.com/spf13/cobra"
)

var (
	yesFlag      bool
	skipPathHint bool
)

// MainCmd is the cobra command registered on the root command.
var MainCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade the hoop CLI to match the connected gateway",
	Long: `Upgrade the hoop CLI to the same version as the connected hoop gateway.

The downloaded binary is installed at $HOME/.hoop/versions/<version>/hoop
and a symlink at $HOME/.hoop/bin/hoop is updated to point at it. Add
$HOME/.hoop/bin to your PATH (ahead of any Homebrew or system path) to
make the active hoop version the one picked up by your shell.`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUpgrade()
	},
}

func init() {
	MainCmd.Flags().BoolVarP(&yesFlag, "yes", "y", false, "Assume yes to interactive prompts (PATH setup)")
	MainCmd.Flags().BoolVar(&skipPathHint, "skip-path-hint", false, "Do not prompt to add $HOME/.hoop/bin to PATH")
}

func runUpgrade() error {
	// `hoop upgrade` is the command we'd recommend in the version-mismatch
	// warning. Suppress the warning here so the user doesn't see it
	// immediately before the "Installing <new>..." banner we print below.
	upgrade.SuppressVersionWarning()

	conf, err := clientconfig.GetClientConfig()
	if err != nil {
		return fmt.Errorf("%v; run `hoop login` first", err)
	}

	gw, err := upgrade.FetchGatewayInfo(conf.ApiURL, conf.Token, conf.TlsCA())
	if err != nil {
		return err
	}
	target := upgrade.NormalizeVersion(gw.Version)

	// Don't print "Gateway reports version <X>" until we know <X> is
	// actually something we can act on; otherwise we double-print the
	// version inside the error message that follows.
	if err := upgrade.ValidateInstallableVersion(target); err != nil {
		return formatUnactionableGatewayVersion(conf.ApiURL, target, err)
	}

	fmt.Printf("Gateway at %s reports version %s\n", conf.ApiURL, target)

	layout, err := upgrade.DefaultLayout()
	if err != nil {
		return err
	}
	store, err := upgrade.LoadStore(layout)
	if err != nil {
		return err
	}

	if store.Active == target {
		if _, err := os.Stat(layout.BinLink); err == nil {
			fmt.Printf("Already on %s (symlink: %s)\n", target, layout.BinLink)
			if !skipPathHint {
				_ = runPathHint(layout)
			}
			return nil
		}
		// store says active but symlink missing — fall through to repair.
	}

	platform, err := upgrade.CurrentPlatform()
	if err != nil {
		return err
	}

	if _, ok := store.Get(target); !ok {
		fmt.Printf("Installing %s for %s ...\n", target, platform)
	} else if _, statErr := os.Stat(layout.VersionBinary(target)); statErr != nil {
		fmt.Printf("Re-installing %s for %s (binary missing on disk) ...\n", target, platform)
	} else {
		fmt.Printf("Version %s already installed; switching active version.\n", target)
	}

	installer := upgrade.NewInstaller(layout)
	entry, err := installer.Install(target, platform, store)
	if err != nil {
		return err
	}
	if err := upgrade.SetActive(layout, store, entry.Version); err != nil {
		return err
	}
	if err := store.Save(layout); err != nil {
		return err
	}

	fmt.Printf("Active hoop version is now %s\n", entry.Version)
	fmt.Printf("Installed at: %s\n", layout.VersionBinary(entry.Version))
	fmt.Printf("Symlink:      %s -> %s\n", layout.BinLink, layout.VersionBinary(entry.Version))

	if !skipPathHint {
		_ = runPathHint(layout)
	}
	return nil
}

// runPathHint checks whether $HOME/.hoop/bin is on PATH; if not, prompts
// (or non-interactively, if -y) to append the export to the shell rc.
func runPathHint(layout upgrade.Layout) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if upgrade.IsPathConfigured(os.Getenv("PATH"), home) {
		return nil
	}

	shell := upgrade.DetectShell(nil)
	rcFile := upgrade.RCFileFor(shell, home)
	line := upgrade.PathExportLine(shell)
	if rcFile == "" || line == "" {
		fmt.Println()
		fmt.Println(styles.KeywordHighlight("Heads up:") + " " + layout.BinDir + " is not in your PATH.")
		fmt.Println("Add it manually to your shell profile, for example:")
		fmt.Println(`  export PATH="$HOME/.hoop/bin:$PATH"`)
		return nil
	}

	fmt.Println()
	fmt.Println(styles.KeywordHighlight("Heads up:") + " " + layout.BinDir + " is not in your PATH.")
	fmt.Printf("The following line will activate the symlinked hoop CLI for new shells:\n")
	fmt.Printf("  %s\n", line)

	if !yesFlag {
		fmt.Printf("Append it to %s now? [y/N] ", rcFile)
		reply, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		reply = strings.ToLower(strings.TrimSpace(reply))
		if reply != "y" && reply != "yes" {
			fmt.Println("Skipped. Add the line yourself when convenient.")
			return nil
		}
	}

	added, err := upgrade.AppendIfMissing(rcFile, line)
	if err != nil {
		return fmt.Errorf("failed updating %s: %w", rcFile, err)
	}
	if !added {
		fmt.Printf("%s already contains the PATH entry; nothing to do.\n", rcFile)
		return nil
	}
	fmt.Printf("Updated %s.\n", rcFile)
	fmt.Println("Open a new shell, or run the appropriate source command, e.g.:")
	switch shell {
	case upgrade.ShellFish:
		fmt.Printf("  source %s\n", rcFile)
	default:
		fmt.Printf("  source %s\n", rcFile)
	}
	return nil
}

// formatUnactionableGatewayVersion turns a ValidateInstallableVersion
// failure into a multi-line, branch-specific message. The goal is to be
// clear about *why* hoop upgrade cannot continue and *what* the user
// should do — without dragging in technical phrases like
// "not a valid semantic version" that aren't actionable.
func formatUnactionableGatewayVersion(apiURL, target string, err error) error {
	floor := upgrade.MinInstallableMinor[1:] // strip leading "v"

	switch {
	case errors.Is(err, upgrade.ErrUnknownGatewayVersion):
		return fmt.Errorf(`the gateway at %s did not report a release version (got %q).

This usually means you're connected to a local development build that wasn't stamped with a release tag.

  - If this is your dev gateway, you don't need to upgrade the CLI.
  - If this is a real deployment, the gateway image is unstripped — rebuild it with a release tag and retry.`,
			apiURL, target)

	case errors.Is(err, upgrade.ErrBelowFloor):
		return fmt.Errorf(`the gateway at %s is on version %s, but the `+"`hoop upgrade`"+` command was only introduced in %s.0.
Hoop CLIs older than %s.0 don't ship this command, so it can't install a matching CLI for you.

What to do:
  - recommended: upgrade the gateway to %s.0 or newer, then re-run `+"`hoop upgrade`"+`.
  - manual: download hoop %s by hand from https://github.com/hoophq/hoop/releases (or via brew). That CLI won't have `+"`hoop upgrade`"+`, so future version changes will also need to be done by hand.`,
			apiURL, target, floor, floor, floor, target)

	case errors.Is(err, upgrade.ErrInvalidVersion):
		return fmt.Errorf(`the gateway at %s returned an unexpected version string (%q).

This usually means the endpoint behind api_url isn't a hoop gateway. Verify the URL with:
  hoop config view`,
			apiURL, target)

	default:
		// Unknown error class — keep the original chain so debugging is
		// still possible without us masking the root cause.
		return fmt.Errorf("hoop upgrade cannot proceed against the gateway at %s: %w", apiURL, err)
	}
}
