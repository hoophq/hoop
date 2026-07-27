package versions

import (
	"errors"
	"fmt"

	clientconfig "github.com/hoophq/hoop/client/config"
	"github.com/hoophq/hoop/client/upgrade"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Install the hoop CLI version reported by the connected gateway",
	Long: `Ask the connected hoop gateway for its version, download the matching
release for this OS/arch, and switch the active hoop CLI to it.

This is the right command when you want the CLI and the gateway to be
on identical versions. If you'd rather track the latest published
release instead, use ` + "`hoop versions upgrade`" + ` (which doesn't
require any gateway configuration).`,
	SilenceUsage: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		return runSync()
	},
}

func runSync() error {
	// The version-mismatch warning would otherwise fire right before
	// the "Installing <new>..." banner we print below. Silence it for
	// this process so the user only sees the upgrade story we're
	// actually telling them.
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
	// actually something we can act on; otherwise the version would
	// appear twice — once in the breadcrumb, once inside the error.
	if err := upgrade.ValidateInstallableVersion(target); err != nil {
		return formatUnactionableGatewayVersion(conf.ApiURL, target, err)
	}

	fmt.Printf("Gateway at %s reports version %s\n", conf.ApiURL, target)
	return installAndActivate(target)
}

// formatUnactionableGatewayVersion turns a ValidateInstallableVersion
// failure into a multi-line, branch-specific message. The goal is to
// be clear about *why* `hoop versions sync` cannot continue and *what*
// the user should do — without dragging in technical phrases like
// "not a valid semantic version" that aren't actionable.
func formatUnactionableGatewayVersion(apiURL, target string, err error) error {
	floor := upgrade.MinInstallableMinor[1:] // strip leading "v"

	switch {
	case errors.Is(err, upgrade.ErrUnknownGatewayVersion):
		return fmt.Errorf(`the gateway at %s did not report a release version (got %q).

This usually means you're connected to a local development build that wasn't stamped with a release tag.

  - If this is your dev gateway, you don't need to sync the CLI.
  - If this is a real deployment, the gateway image is unstripped — rebuild it with a release tag and retry.
  - To install the latest published release instead, run `+"`hoop versions upgrade`"+`.`,
			apiURL, target)

	case errors.Is(err, upgrade.ErrBelowWindowsFloor):
		winFloor := upgrade.MinInstallableVersionWindows[1:] // strip leading "v"
		return fmt.Errorf(`the gateway at %s is on version %s, but `+"`hoop versions`"+` only supports Windows from %s onward.
Hoop CLIs older than %s don't manage themselves correctly on Windows, so a matching CLI can't be installed for you here.

What to do:
  - recommended: upgrade the gateway to %s or newer, then re-run `+"`hoop versions sync`"+`.
  - manual: download a Windows build of hoop %s by hand from https://github.com/hoophq/hoop/releases. That CLI won't be able to self-manage on Windows, so future version changes will also need to be done by hand.`,
			apiURL, target, winFloor, winFloor, winFloor, target)

	case errors.Is(err, upgrade.ErrBelowFloor):
		return fmt.Errorf(`the gateway at %s is on version %s, but the `+"`hoop versions sync`"+` command was only introduced in %s.0.
Hoop CLIs older than %s.0 don't ship this command, so it can't install a matching CLI for you.

What to do:
  - recommended: upgrade the gateway to %s.0 or newer, then re-run `+"`hoop versions sync`"+`.
  - manual: download hoop %s by hand from https://github.com/hoophq/hoop/releases (or via brew). That CLI won't have `+"`hoop versions sync`"+`, so future version changes will also need to be done by hand.`,
			apiURL, target, floor, floor, floor, target)

	case errors.Is(err, upgrade.ErrInvalidVersion):
		return fmt.Errorf(`the gateway at %s returned an unexpected version string (%q).

This usually means the endpoint behind api_url isn't a hoop gateway. Verify the URL with:
  hoop config view`,
			apiURL, target)

	default:
		// Unknown error class — keep the original chain so debugging
		// is still possible without us masking the root cause.
		return fmt.Errorf("hoop versions sync cannot proceed against the gateway at %s: %w", apiURL, err)
	}
}
