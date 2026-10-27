// This binary switches profiles in config.toml and runs the installed hoop
// release for every other command. The release reads a flat config file.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hoophq/hoop/common/clientconfig"
)

func main() {
	path, err := defaultConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code, err := dispatch(os.Args[1:], "", path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

func defaultConfig() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	// The installed release always reads this path. It does not read HOOP_CONFIG.
	return filepath.Join(home, ".hoop", "config.toml"), nil
}

func dispatch(args []string, real, configPath string) (int, error) {
	if err := recoverBackup(configPath); err != nil {
		return 1, err
	}
	if configHelp(args) {
		printConfigHelp()
		return 0, nil
	}
	cmd, rest, ok := profileCmd(args)
	if !ok {
		return delegate(real, configPath, args)
	}
	if helpFlag(rest) {
		printProfileHelp(cmd)
		return 0, nil
	}
	return runProfile(cmd, rest, configPath)
}

func configHelp(args []string) bool {
	if len(args) == 0 || args[0] != "config" {
		return false
	}
	if len(args) == 1 {
		return true
	}
	return len(args) == 2 && (args[1] == "-h" || args[1] == "--help" || args[1] == "help")
}

func helpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func printConfigHelp() {
	fmt.Print(`Manage the hoop configuration file.

Switch profiles with "hoop config use-context <name>".
Profiles are [profile <name>] sections in the same config.toml.

Usage:
  hoop config [command]

Available Commands:
`)
	printCommandRows([][2]string{
		{"clear", "Delete the client hoop configuration file if exists"},
		{"create", "Creates or override a client hoop configuration file"},
		{"current-context", "Print the active Hoop profile"},
		{"delete-context", "Delete a profile from config.toml"},
		{"get-contexts", "List Hoop profiles in the config file"},
		{"set-context", "Save the active credentials as a profile in config.toml"},
		{"use-context", "Switch the active profile in config.toml"},
		{"view", "Show the current configuration"},
	})
	fmt.Print(`
Flags:
  -h, --help   help for config

Global Flags:
      --debug             Turn on debugging
      --debug-grpc        Turn on debugging of gRPC (http2) if applicable
      --skip-tls-verify   Skip TLS verification when connecting to Hoop components

Use "hoop config [command] --help" for more information about a command.
`)
}

func printProfileHelp(cmd string) {
	switch cmd {
	case "list":
		fmt.Print(`List [profile <name>] sections in the client config file.
The selected profile is the account used by other commands.

Usage:
  hoop config get-contexts [flags]

Aliases:
  get-contexts, contexts, ls

`)
	case "current":
		fmt.Print(`Print the active Hoop profile

Usage:
  hoop config current-context [flags]

`)
	case "use":
		fmt.Print(`Select a [profile <name>] section in the client config file.
The other profiles stay in that same file.

Usage:
  hoop config use-context <name> [flags]

Aliases:
  use-context, use

Example:
  hoop config use-context profile2

`)
	case "set":
		fmt.Print(`Save the active credentials as a profile in config.toml

Usage:
  hoop config set-context <name> [flags]

Example:
  hoop config set-context profile1

`)
	case "delete":
		fmt.Print(`Delete one [profile <name>] section from the client config file.
The active profile cannot be deleted. Switch away from it first.

Usage:
  hoop config delete-context <name> [flags]

Example:
  hoop config delete-context old

`)
	default:
		fmt.Fprintf(os.Stderr, "unknown profile command %q\n", cmd)
		return
	}
	fmt.Print(`Flags:
  -h, --help   help for this command

Global Flags:
      --debug             Turn on debugging
      --debug-grpc        Turn on debugging of gRPC (http2) if applicable
      --skip-tls-verify   Skip TLS verification when connecting to Hoop components
`)
}

func printCommandRows(rows [][2]string) {
	width := 0
	for _, row := range rows {
		if len(row[0]) > width {
			width = len(row[0])
		}
	}
	for _, row := range rows {
		fmt.Printf("  %-*s   %s\n", width, row[0], row[1])
	}
}

func profileCmd(args []string) (string, []string, bool) {
	if len(args) < 2 || args[0] != "config" {
		return "", nil, false
	}
	switch args[1] {
	case "get-contexts", "contexts", "ls":
		return "list", args[2:], true
	case "current-context":
		return "current", args[2:], true
	case "use-context", "use":
		return "use", args[2:], true
	case "set-context":
		return "set", args[2:], true
	case "delete-context":
		return "delete", args[2:], true
	default:
		return "", nil, false
	}
}

func runProfile(cmd string, rest []string, path string) (int, error) {
	switch cmd {
	case "list":
		if len(rest) != 0 {
			return 1, fmt.Errorf("get-contexts takes no arguments")
		}
		return listProfiles(path)
	case "current":
		if len(rest) != 0 {
			return 1, fmt.Errorf("current-context takes no arguments")
		}
		name, err := clientconfig.CurrentProfile(path)
		if err != nil {
			return 1, err
		}
		fmt.Println(name)
	case "use":
		if len(rest) != 1 {
			return 1, fmt.Errorf("use-context takes one profile name")
		}
		item, err := clientconfig.UseProfile(path, rest[0])
		if err != nil {
			return 1, err
		}
		fmt.Printf("Switched to profile %q (%s).\n", item.Name, item.APIURL)
	case "set":
		if len(rest) != 1 {
			return 1, fmt.Errorf("set-context takes one profile name")
		}
		item, err := clientconfig.SaveProfile(path, rest[0])
		if err != nil {
			return 1, err
		}
		fmt.Printf("Saved profile %q (%s).\n", item.Name, item.APIURL)
	case "delete":
		if len(rest) != 1 {
			return 1, fmt.Errorf("delete-context takes one profile name")
		}
		if err := clientconfig.DeleteProfile(path, rest[0]); err != nil {
			return 1, err
		}
		fmt.Printf("Deleted profile %q.\n", rest[0])
	default:
		return 1, fmt.Errorf("unknown profile command %q", cmd)
	}
	return 0, nil
}

func listProfiles(path string) (int, error) {
	list, err := clientconfig.ListProfiles(path)
	if err != nil {
		return 1, err
	}
	if len(list) == 0 {
		fmt.Printf("No profiles in %s.\n", path)
		fmt.Println("Add a [profile <name>] section, or run `hoop config set-context <name>`.")
		return 0, nil
	}
	fmt.Printf("File: %s\n", path)
	fmt.Printf("%s\t%s\t%s\n", " ", "NAME", "API URL")
	for _, item := range list {
		mark := " "
		if item.Current {
			mark = "*"
		}
		fmt.Printf("%s\t%s\t%s\n", mark, item.Name, item.APIURL)
	}
	return 0, nil
}

func delegate(real, configPath string, args []string) (int, error) {
	if real == "" {
		var err error
		real, err = runtimeBinary()
		if err != nil {
			return 1, err
		}
	}
	original, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return runChild(real, args), nil
	}
	if err != nil {
		return 1, err
	}
	doc, err := clientconfig.ParseProfileFile(original)
	if err != nil {
		return 1, err
	}
	if !doc.HasProfiles() {
		return runChild(real, args), nil
	}
	active, err := doc.Active()
	if err != nil {
		return 1, err
	}
	flat := clientconfig.MarshalFlat(active)
	if err := writeFile(backupPath(configPath), original); err != nil {
		return 1, err
	}
	if err := writeFile(configPath, flat); err != nil {
		return 1, err
	}
	code := runChild(real, args)
	if err := finish(configPath, flat); err != nil {
		return code, err
	}
	return code, nil
}

func finish(configPath string, written []byte) error {
	cur, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return os.Remove(backupPath(configPath))
	}
	if err != nil {
		return err
	}
	if bytes.Equal(cur, written) {
		original, err := os.ReadFile(backupPath(configPath))
		if err != nil {
			return err
		}
		if err := writeFile(configPath, original); err != nil {
			return err
		}
		return os.Remove(backupPath(configPath))
	}
	return recoverBackup(configPath)
}

func recoverBackup(configPath string) error {
	bak := backupPath(configPath)
	original, err := os.ReadFile(bak)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cur, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return os.Remove(bak)
	}
	if err != nil {
		return err
	}
	if bytes.Equal(cur, original) {
		return os.Remove(bak)
	}
	doc, err := clientconfig.ParseProfileFile(original)
	if err != nil || !doc.HasProfiles() {
		if werr := writeFile(configPath, original); werr != nil {
			return werr
		}
		return os.Remove(bak)
	}
	curDoc, err := clientconfig.ParseProfileFile(cur)
	if err != nil {
		if werr := writeFile(configPath, original); werr != nil {
			return werr
		}
		return os.Remove(bak)
	}
	if curDoc.HasProfiles() {
		return os.Remove(bak)
	}
	flat, ok := curDoc.Flat()
	if !ok || flat.APIURL == "" {
		if werr := writeFile(configPath, original); werr != nil {
			return werr
		}
		return os.Remove(bak)
	}
	if err := writeFile(configPath, original); err != nil {
		return err
	}
	if err := clientconfig.UpdateActiveProfile(configPath, flat); err != nil {
		return err
	}
	return os.Remove(bak)
}

func backupPath(configPath string) string {
	return configPath + ".profiles-backup"
}

func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".cfg-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func runChild(bin string, args []string) int {
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigs)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case err := <-done:
			if err == nil {
				return 0
			}
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return exit.ExitCode()
			}
			fmt.Fprintln(os.Stderr, err)
			return 1
		case sig := <-sigs:
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}
}

func runtimeBinary() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, ".hoop")
	self, err := os.Executable()
	if err == nil {
		if resolved, rerr := filepath.EvalSymlinks(self); rerr == nil {
			self = resolved
		}
	}
	version := activeVersion(filepath.Join(root, "versions.toml"))
	if version != "" && !strings.Contains(version, "/") && !strings.Contains(version, "..") {
		candidate := filepath.Join(root, "versions", version, "hoop")
		if path, ok := usableBinary(candidate, self); ok {
			return path, nil
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "versions"))
	if err != nil {
		return "", fmt.Errorf("no released hoop binary in %s", filepath.Join(root, "versions"))
	}
	var found string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(root, "versions", entry.Name(), "hoop")
		path, ok := usableBinary(candidate, self)
		if ok {
			found = path
		}
	}
	if found == "" {
		return "", fmt.Errorf("no released hoop binary in %s", filepath.Join(root, "versions"))
	}
	return found, nil
}

func usableBinary(path, self string) (string, bool) {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Mode()&0111 == 0 {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	if self != "" && resolved == self {
		return "", false
	}
	return path, true
}

func activeVersion(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		text := strings.TrimSpace(string(line))
		key, raw, ok := strings.Cut(text, "=")
		if !ok || strings.TrimSpace(key) != "active" {
			continue
		}
		raw = strings.TrimSpace(raw)
		if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
			raw = raw[1 : len(raw)-1]
		}
		return raw
	}
	return ""
}
