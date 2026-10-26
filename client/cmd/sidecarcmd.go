package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/spf13/cobra"
)

// `hoop sidecar` reads and changes what this machine holds for its sidecar:
// the logins `hoop start sidecar` saved, one per control plane. Running the
// sidecar stays `hoop start sidecar`.

var (
	sidecarStatusTokenFlag bool
	sidecarLogoutPlaneFlag string
	sidecarLogoutAllFlag   bool
)

var sidecarCmd = &cobra.Command{
	Use:   "sidecar",
	Short: "Show or change the sidecar's login on this machine",
	Long: `The sidecar logs in to a control plane the first time it runs and keeps its
token here, one per control plane, in ` + "`" + SidecarStateDirEnv + "`" + ` or ~/.hoop/sidecar.

Run the sidecar with: hoop start sidecar`,
}

var sidecarStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the control planes this sidecar is logged in to",
	Example: `  hoop sidecar status
  hoop sidecar status --token   # print the token, for HOOP_SIDECAR_TOKEN on a headless host`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return printSidecarStatus(cmd.OutOrStdout(), sidecarStateDir(), os.Getenv, sidecarStatusTokenFlag)
	},
}

var sidecarLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Forget this machine's sidecar login",
	Long: `Forgets the sidecar's token on this machine. The next hoop start sidecar logs in
again. The sidecar stays in its organization: delete it in the web app to remove
it there, which also stops its token everywhere.`,
	Example: `  hoop sidecar logout
  hoop sidecar logout --control-plane https://cp.example.com
  hoop sidecar logout --all`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return sidecarLogout(cmd.OutOrStdout(), sidecarStateDir(), sidecarLogoutPlaneFlag, sidecarLogoutAllFlag)
	},
}

func printSidecarStatus(out io.Writer, dir string, getenv func(string) string, showToken bool) error {
	logins, err := listSidecarLogins(dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Logins on this machine (%s):\n", filepath.Join(dir, sidecarLoginFile))
	if len(logins) == 0 {
		fmt.Fprintln(out, "  none. hoop start sidecar logs in on its next run.")
	}
	for _, l := range logins {
		kind := "Enterprise control plane"
		if normalizePlaneURL(l.URL) == normalizePlaneURL(DefaultSidecarControlPlaneURL) {
			kind = "hoop, free plan"
		}
		fmt.Fprintf(out, "\n  %s (%s)\n", l.URL, kind)
		fmt.Fprintf(out, "    sidecar  %s\n", l.Name)
		if l.Config != "" {
			fmt.Fprintf(out, "    config   %s\n", l.Config)
		}
		if l.Page != "" {
			fmt.Fprintf(out, "    page     %s\n", l.Page)
		}
		if showToken {
			fmt.Fprintf(out, "    token    %s\n", l.Token)
		}
	}
	// The environment outranks this folder, so say when it decides instead.
	var env []string
	if v := getenv(daemon.ControlPlaneURLEnv); v != "" {
		env = append(env, daemon.ControlPlaneURLEnv+"="+v)
	}
	if getenv(daemon.SidecarTokenEnv) != "" {
		env = append(env, daemon.SidecarTokenEnv+" is set")
	}
	if len(env) > 0 {
		fmt.Fprintf(out, "\nThe environment decides first: %s\n", strings.Join(env, ", "))
	}
	return nil
}

func sidecarLogout(out io.Writer, dir, plane string, all bool) error {
	switch {
	case all && plane != "":
		return fmt.Errorf("pass --control-plane or --all, not both")
	case all:
		removed, err := removeSidecarLogin(dir, "")
		if err != nil {
			return err
		}
		if !removed {
			fmt.Fprintln(out, "No sidecar login on this machine.")
			return nil
		}
		fmt.Fprintln(out, "Forgot every sidecar login on this machine.")
		return nil
	}
	if plane == "" {
		logins, err := listSidecarLogins(dir)
		if err != nil {
			return err
		}
		switch len(logins) {
		case 0:
			fmt.Fprintln(out, "No sidecar login on this machine.")
			return nil
		case 1:
			plane = logins[0].URL
		default:
			var urls []string
			for _, l := range logins {
				urls = append(urls, l.URL)
			}
			return fmt.Errorf("this machine is logged in to %d control planes (%s); "+
				"pass --control-plane <url> or --all", len(logins), strings.Join(urls, ", "))
		}
	}
	removed, err := removeSidecarLogin(dir, plane)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no sidecar login for %s on this machine", plane)
	}
	fmt.Fprintf(out, "Forgot the sidecar login for %s. The sidecar stays in its organization; "+
		"delete it in the web app to remove it there.\n", normalizePlaneURL(plane))
	return nil
}

func init() {
	sidecarStatusCmd.Flags().BoolVar(&sidecarStatusTokenFlag, "token", false,
		"Print each token too, to set "+daemon.SidecarTokenEnv+" on a headless host")
	sidecarLogoutCmd.Flags().StringVar(&sidecarLogoutPlaneFlag, "control-plane", "",
		"The control plane to log out of, when this machine holds more than one")
	sidecarLogoutCmd.Flags().BoolVar(&sidecarLogoutAllFlag, "all", false, "Forget every sidecar login on this machine")
	sidecarCmd.AddCommand(sidecarStatusCmd, sidecarLogoutCmd)
	rootCmd.AddCommand(sidecarCmd)
}
