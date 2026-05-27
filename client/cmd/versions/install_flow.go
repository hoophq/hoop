package versions

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/hoophq/hoop/client/cmd/styles"
	"github.com/hoophq/hoop/client/upgrade"
)

// installFlags holds the toggles shared by `hoop versions sync` and
// `hoop versions upgrade`. Both commands download a target version,
// switch the active symlink, and optionally walk the user through PATH
// setup, so they share the same flag surface.
var (
	yesFlag      bool
	skipPathHint bool
)

// installAndActivate is the shared install pipeline used by both the
// gateway-sync and the latest-version flows. The target must already
// be normalized (no leading "v") and validated for installability —
// the calling command owns the framing of any "not installable" error.
//
// The function prints the same progress lines regardless of which
// upstream produced the target, so the user-visible output is
// consistent between `hoop versions sync` and `hoop versions upgrade`.
func installAndActivate(target string) error {
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
		// Store thinks this is active but the symlink is gone; fall
		// through to repair it.
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

// runPathHint checks whether $HOME/.hoop/bin is on PATH; if not, it
// prompts the user (or, with -y, applies non-interactively) to append
// the appropriate export line to their shell rc file. Returning an
// error is intentionally non-fatal at the call site: a missing PATH
// entry doesn't undo a successful install, it just means the user has
// to invoke the symlink path directly until they fix it.
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
	fmt.Println("Open a new shell, or source the file:")
	fmt.Printf("  source %s\n", rcFile)
	return nil
}
