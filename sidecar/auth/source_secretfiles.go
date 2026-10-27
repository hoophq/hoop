package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/session"
)

// maxSecretFileBytes bounds a user or password file. A password is tens of
// bytes; anything near this is the wrong file, and reading it would put an
// arbitrary file's bytes in a login.
const maxSecretFileBytes = 16 << 10

// secretFilesSource reads a pool entry's user and password from files under
// one directory, per login, so a Secret the platform rotated in place is
// what the next login presents. A password or key file readable by group or
// other is refused: a Kubernetes Secret mounts 0644 unless told otherwise,
// and a relay that read it anyway would hide that from the operator.
type secretFilesSource struct {
	dir string
}

func buildSecretFilesSource(cfg *daemon.CredentialSourcesConfig) (daemon.CredentialSource, error) {
	if cfg == nil || cfg.SecretFiles == nil || cfg.SecretFiles.Dir == "" {
		return nil, errors.New("credentials.secret_files.dir is not configured")
	}
	return &secretFilesSource{dir: filepath.Clean(cfg.SecretFiles.Dir)}, nil
}

func (s *secretFilesSource) Credential(_ context.Context, e daemon.CredentialEntry, _ session.Identity) (daemon.Credential, error) {
	if err := daemon.ValidateSecretFilesEntry(s.dir, e); err != nil {
		return daemon.Credential{}, fmt.Errorf("credential %q: %w", e.Name, err)
	}
	user := e.User
	if e.UserFile != "" {
		raw, err := s.read(e.UserFile, false)
		if err != nil {
			return daemon.Credential{}, fmt.Errorf("credential %q: user_file: %w", e.Name, err)
		}
		user = raw
	}
	password, err := s.read(e.PasswordFile, true)
	if err != nil {
		return daemon.Credential{}, fmt.Errorf("credential %q: password_file: %w", e.Name, err)
	}
	if e.UserFile != "" && user == "" {
		return daemon.Credential{}, fmt.Errorf("credential %q: user_file %s is empty", e.Name, e.UserFile)
	}
	return daemon.Credential{User: user, Password: password}, nil
}

// read returns a file's content with surrounding whitespace removed, the
// newline an editor or `echo` leaves above all. secret refuses a mode that
// lets group or other read it. Symlinks are followed on both the directory
// and the file, and the file has to land inside the directory: a Kubernetes
// mount links through ..data inside the directory, which passes; a link
// that leaves it reads a file the block never put in reach, and is refused.
func (s *secretFilesSource) read(file string, secret bool) (string, error) {
	full, err := daemon.ResolveSecretFile(s.dir, file)
	if err != nil {
		return "", err
	}
	dir, err := filepath.EvalSymlinks(s.dir)
	if err != nil {
		return "", fmt.Errorf("credentials.secret_files.dir %s: %w", s.dir, err)
	}
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(dir, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s resolves to %s, outside credentials.secret_files.dir %s", file, real, s.dir)
	}
	return readSecretFile(real, secret)
}

// readSecretFile is the bounded, mode-checked read every file-backed source
// shares. Errors name the path and never the content.
func readSecretFile(path string, secret bool) (string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = fh.Close() }()
	info, err := fh.Stat()
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	if secret {
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			return "", fmt.Errorf("%s is readable by group or other (mode %04o); mount it 0400", path, mode)
		}
	}
	raw, err := io.ReadAll(io.LimitReader(fh, maxSecretFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) > maxSecretFileBytes {
		return "", fmt.Errorf("%s holds more than %d bytes", path, maxSecretFileBytes)
	}
	return string(bytes.TrimSpace(raw)), nil
}
