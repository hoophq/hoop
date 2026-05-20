package upgrade

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ShellKind identifies a supported user shell. Currently we only generate
// rc-file snippets for shells whose syntax we can write correctly.
type ShellKind string

const (
	ShellZsh     ShellKind = "zsh"
	ShellBash    ShellKind = "bash"
	ShellFish    ShellKind = "fish"
	ShellUnknown ShellKind = ""
)

// DetectShell inspects the SHELL env var and returns the matching kind.
// Returns ShellUnknown if it can't determine the shell.
func DetectShell(env func(string) string) ShellKind {
	if env == nil {
		env = os.Getenv
	}
	sh := env("SHELL")
	base := filepath.Base(strings.TrimSpace(sh))
	switch base {
	case "zsh":
		return ShellZsh
	case "bash":
		return ShellBash
	case "fish":
		return ShellFish
	default:
		return ShellUnknown
	}
}

// RCFileFor returns the absolute path of the rc file we should append to
// for the given shell, rooted at home. Returns an empty string for unknown
// shells.
//
// For bash we prefer $HOME/.bashrc if it exists, falling back to
// $HOME/.bash_profile (the conventional login-shell file on macOS).
func RCFileFor(kind ShellKind, home string) string {
	switch kind {
	case ShellZsh:
		return filepath.Join(home, ".zshrc")
	case ShellBash:
		bashrc := filepath.Join(home, ".bashrc")
		if _, err := os.Stat(bashrc); err == nil {
			return bashrc
		}
		return filepath.Join(home, ".bash_profile")
	case ShellFish:
		return filepath.Join(home, ".config", "fish", "config.fish")
	default:
		return ""
	}
}

// PathExportLine returns the shell-specific line that prepends
// $HOME/.hoop/bin to PATH. The fish line uses fish_add_path which is
// idempotent within fish itself.
func PathExportLine(kind ShellKind) string {
	switch kind {
	case ShellFish:
		return `fish_add_path -p "$HOME/.hoop/bin"`
	case ShellZsh, ShellBash:
		return `export PATH="$HOME/.hoop/bin:$PATH"`
	default:
		return ""
	}
}

// IsPathConfigured reports whether the given PATH string already contains
// the hoop bin directory (resolved against home).
func IsPathConfigured(path, home string) bool {
	hoopBin := filepath.Clean(filepath.Join(home, ".hoop", "bin"))
	for _, entry := range filepath.SplitList(path) {
		entry = strings.ReplaceAll(entry, "$HOME", home)
		entry = expandTilde(entry, home)
		if filepath.Clean(entry) == hoopBin {
			return true
		}
	}
	return false
}

func expandTilde(p, home string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, strings.TrimPrefix(p, "~/"))
	}
	return p
}

// AppendIfMissing appends line (with a leading marker comment) to rcFile
// if the file does not already contain that exact line. Returns true if
// the file was modified.
//
// The function is idempotent: running it twice never duplicates the line.
// It creates the file (and its parent directory) if missing, using 0700
// for the directory and 0600 for the new file.
func AppendIfMissing(rcFile, line string) (bool, error) {
	if rcFile == "" || line == "" {
		return false, fmt.Errorf("rcFile and line must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(rcFile), 0700); err != nil {
		return false, fmt.Errorf("failed creating rc dir: %w", err)
	}

	if hasLine, err := fileContainsLine(rcFile, line); err != nil {
		return false, err
	} else if hasLine {
		return false, nil
	}

	f, err := os.OpenFile(rcFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return false, fmt.Errorf("failed opening %s: %w", rcFile, err)
	}
	defer f.Close()

	block := "\n# Added by `hoop versions` to expose the active hoop CLI version\n" + line + "\n"
	if _, err := f.WriteString(block); err != nil {
		return false, fmt.Errorf("failed writing to %s: %w", rcFile, err)
	}
	return true, nil
}

func fileContainsLine(path, line string) (bool, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed opening %s: %w", path, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == strings.TrimSpace(line) {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("failed scanning %s: %w", path, err)
	}
	return false, nil
}
