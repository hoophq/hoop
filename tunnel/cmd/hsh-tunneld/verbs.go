package main

// This file is the verb dispatcher for the hsh-tunneld binary. Calling
// the binary with no arguments runs the daemon (legacy behaviour);
// passing a subcommand routes to one of the management verbs below.
//
// Why hand-rolled instead of cobra
//
// We deliberately keep the dispatcher tiny and dependency-free:
//
//   1. Cobra would pull a non-trivial amount of code into a binary
//      whose primary cost we measure (size + cold start) — the daemon
//      runs at boot, and the install verb may run inside a brew/deb
//      post-install where every megabyte counts.
//
//   2. The verb set is small and unlikely to grow much. install /
//      uninstall / validate-config / status / start / stop covers
//      every interaction a system-service needs; richer commands
//      already live in the hsh CLI.
//
//   3. Per-verb flag.FlagSet gives us crisp `hsh-tunneld install
//      --help` output without macros or generators.
//
// Layout
//
// Each verb is a small Run function with its own FlagSet. The top-level
// dispatch is a single switch in main.go. Adding a verb is: add a case,
// add a Run function in this file (or a sibling file if the verb is
// big), keep main.go's switch alphabetical.

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/hoophq/hoop/tunnel/daemonconfig"
	"github.com/hoophq/hoop/tunnel/service"
)

// usage prints the top-level help block for `hsh-tunneld` with no
// args (or `hsh-tunneld help`). The verb list mirrors the switch in
// main.go.
func usage(w *os.File) {
	fmt.Fprint(w, `hsh-tunneld — Hoop Tunnel daemon

Usage:
  hsh-tunneld [daemon flags]           run the daemon (default)
  hsh-tunneld install   [flags]        register as a system service
  hsh-tunneld uninstall [flags]        remove the system service
  hsh-tunneld validate-config [flags]  parse the config file (no side effects)
  hsh-tunneld status                   show the service-manager-visible state
  hsh-tunneld start                    start the registered service
  hsh-tunneld stop                     stop the registered service
  hsh-tunneld version                  print version and exit
  hsh-tunneld help                     this message

The install / uninstall / start / stop verbs require root (or an
elevated shell on Windows). validate-config and status do not.

Run any verb with --help for verb-specific flags.

The daemon (no verb) is what the system-service unit starts. For dev
runs it can also be invoked directly:

  sudo HOOP_APIURL=... HOOP_TOKEN=... hsh-tunneld --tld hoop

`)
}

// runInstall registers hsh-tunneld with the platform service manager.
// Idempotent. Requires root. The default behaviour copies the running
// binary to /usr/local/bin/hsh-tunneld so a `curl | tar | sudo
// ./hsh-tunneld install` flow works without an extra `cp` step.
func runInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	def := service.DefaultOptions()
	binaryPath := fs.String("binary-path", def.BinaryPath, "destination path for the daemon binary")
	configPath := fs.String("config-file", def.ConfigPath, "path of the daemon's TOML config")
	socketPath := fs.String("socket-path", def.SocketPath, "path of the IPC unix socket the daemon will bind")
	groupName := fs.String("group", def.GroupName, "OS group that owns the runtime directory + socket")
	noCopy := fs.Bool("no-copy", false, "skip copying the running binary to --binary-path (use for packager-managed installs where the binary is already in place)")
	noGroup := fs.Bool("no-create-group", false, "skip creating the OS group (use for packager-managed installs)")
	noEnable := fs.Bool("no-enable", false, "skip enabling the service for start at boot")
	noStart := fs.Bool("no-start", false, "skip starting the service after registration")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: hsh-tunneld install [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := service.DefaultOptions()
	opts.BinaryPath = *binaryPath
	opts.ConfigPath = *configPath
	opts.SocketPath = *socketPath
	opts.GroupName = *groupName
	opts.CopyBinary = !*noCopy
	opts.CreateGroup = !*noGroup
	opts.EnableOnBoot = !*noEnable
	opts.StartAfterInstall = !*noStart

	mgr := service.New()
	if !mgr.IsElevated() {
		return fmt.Errorf("install requires root (run with sudo). Detected platform: %s", mgr.PlatformName())
	}
	fmt.Printf("hsh-tunneld: installing via %s\n", mgr.PlatformName())
	fmt.Printf("  binary: %s\n", opts.BinaryPath)
	fmt.Printf("  config: %s\n", opts.ConfigPath)
	fmt.Printf("  socket: %s\n", opts.SocketPath)
	fmt.Printf("  group:  %s\n", opts.GroupName)

	if err := mgr.Install(opts); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	st, _ := mgr.Status()
	fmt.Printf("\nhsh-tunneld: install OK — service is %s\n", st)
	if opts.AddInvokingUser && opts.GroupName != "" {
		if u := os.Getenv("SUDO_USER"); u != "" && u != "root" {
			// We added them to the group during Install. Membership only
			// applies to new login sessions, so be explicit about the
			// relaunch requirement rather than implying it works instantly.
			fmt.Printf("\nAdded %q to the %q group — the hsh CLI and tray can now\n", u, opts.GroupName)
			fmt.Printf("drive the daemon without sudo. Group membership applies to NEW\n")
			fmt.Printf("login sessions, so relaunch your terminal/tray (or log out and\n")
			fmt.Printf("back in), then run: hsh tunnel status\n")
		} else {
			// Real-root login / packager: no invoking user to add.
			fmt.Printf("\nTo drive the daemon without sudo, add a user to the %q group:\n", opts.GroupName)
			fmt.Printf("  sudo dscl . -append /Groups/%s GroupMembership <user>   # macOS\n", opts.GroupName)
			fmt.Printf("  sudo usermod -aG %s <user>                              # Linux\n", opts.GroupName)
			fmt.Printf("Then relaunch their session and run: hsh tunnel status\n")
		}
	}
	return nil
}

// runUninstall stops and removes the service. --purge additionally
// removes user-state (config, group, binary). Idempotent: running it
// twice in a row is fine.
func runUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	def := service.DefaultPurgeOptions()
	binaryPath := fs.String("binary-path", def.BinaryPath, "binary path to remove (only used with --purge or --remove-binary)")
	configPath := fs.String("config-file", def.ConfigPath, "config path to remove (only used with --purge or --remove-config)")
	groupName := fs.String("group", def.GroupName, "group to delete (only used with --purge or --remove-group)")
	purge := fs.Bool("purge", false, "remove all user-state: config, group, binary")
	removeConfig := fs.Bool("remove-config", false, "remove the config directory")
	removeGroup := fs.Bool("remove-group", false, "remove the OS group")
	removeBinary := fs.Bool("remove-binary", false, "remove the installed binary at --binary-path")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: hsh-tunneld uninstall [flags]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	opts := service.PurgeOptions{
		BinaryPath: *binaryPath,
		ConfigPath: *configPath,
		GroupName:  *groupName,
	}
	if *purge {
		opts.RemoveConfig = true
		opts.RemoveGroup = true
		opts.RemoveBinary = true
	}
	if *removeConfig {
		opts.RemoveConfig = true
	}
	if *removeGroup {
		opts.RemoveGroup = true
	}
	if *removeBinary {
		opts.RemoveBinary = true
	}

	mgr := service.New()
	if !mgr.IsElevated() {
		return fmt.Errorf("uninstall requires root (run with sudo). Detected platform: %s", mgr.PlatformName())
	}

	fmt.Printf("hsh-tunneld: uninstalling via %s\n", mgr.PlatformName())
	if err := mgr.Uninstall(opts); err != nil {
		return fmt.Errorf("uninstall: %w", err)
	}
	fmt.Printf("hsh-tunneld: uninstall OK\n")
	return nil
}

// runValidateConfig parses the daemon's TOML config file and returns
// any errors. It is meant to be invoked from systemd's ExecStartPre
// clause so a malformed config produces an actionable journal entry
// before the daemon tries to bind sockets.
//
// We deliberately keep this free of any non-config code (no IPC, no
// netstack, no gateway dial) so it stays fast and side-effect-free.
// validate-config running on a fresh install with an empty file is a
// success — Load returns the zero Config in that case and we accept
// it.
func runValidateConfig(args []string) error {
	fs := flag.NewFlagSet("validate-config", flag.ExitOnError)
	configFile := fs.String("config-file", daemonconfig.DefaultConfigPathPlatform(), "path of the daemon's TOML config")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: hsh-tunneld validate-config [--config-file path]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := daemonconfig.Load(*configFile)
	if err != nil {
		return fmt.Errorf("config %q invalid: %w", *configFile, err)
	}
	// Cross-field validation lives in the Config type itself; for now
	// we surface just enough detail for journal viewers to act on it.
	fmt.Printf("config %s OK (api_url=%q logged_in=%v)\n", *configFile, cfg.APIURL, cfg.LoggedIn())
	return nil
}

// runServiceStatus calls into the service Manager and prints the
// service-manager-visible state. Distinct from `hsh tunnel status`
// (which calls /v1/status over IPC); use this one when you don't have
// a running daemon and want to know whether the unit is in place.
func runServiceStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: hsh-tunneld status\n")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	mgr := service.New()
	st, err := mgr.Status()
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}
	fmt.Printf("%s\n", st)
	return nil
}

// runServiceStart is a convenience wrapper around `systemctl start
// hsh-tunneld` / `launchctl ...`. Useful when the operator does not
// want to remember which command the platform service manager uses.
func runServiceStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	mgr := service.New()
	if !mgr.IsElevated() {
		return fmt.Errorf("start requires root (run with sudo)")
	}
	if err := mgr.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	fmt.Printf("hsh-tunneld started\n")
	return nil
}

// runServiceStop is the counterpart to runServiceStart.
func runServiceStop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	mgr := service.New()
	if !mgr.IsElevated() {
		return fmt.Errorf("stop requires root (run with sudo)")
	}
	if err := mgr.Stop(); err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	fmt.Printf("hsh-tunneld stopped\n")
	return nil
}

// runVersion prints the version block and exits. We include the
// platform so packagers can verify their build target.
func runVersion(_ []string, logger *log.Logger) error {
	_ = logger
	fmt.Printf("%s\n", userAgent())
	return nil
}
