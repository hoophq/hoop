package sidecartui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PlaneCredentialFile is the file, in the sidecar's save directory, that
// holds the Control Plane URL and the sidecar token a pairing (or the
// Connect page) produced. Every later `hoop start sidecar` uses it, so a
// person signs in once.
const PlaneCredentialFile = "controlplane.json"

// PlaneCredential is a sidecar's way back to its Control Plane. URL and Token
// always go together: the daemon refuses one without the other.
type PlaneCredential struct {
	URL   string `json:"control_plane_url"`
	Token string `json:"token"`
	// Name and ID are the sidecar the token belongs to, for messages only.
	Name string `json:"name,omitempty"`
	ID   string `json:"id,omitempty"`
}

// LoadPlaneCredential reads the stored credential from dir. A missing file is
// nil, nil: the person has not signed in yet. A file that does not hold both
// halves is an error, so a damaged file is never read as signed in.
func LoadPlaneCredential(dir string) (*PlaneCredential, error) {
	if dir == "" {
		return nil, nil
	}
	path := filepath.Join(dir, PlaneCredentialFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var c PlaneCredential
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s is not a Control Plane credential: %w", path, err)
	}
	c.URL, c.Token = strings.TrimRight(strings.TrimSpace(c.URL), "/"), strings.TrimSpace(c.Token)
	if c.URL == "" || c.Token == "" {
		return nil, fmt.Errorf("%s holds no Control Plane URL or no token; sign in again to replace it", path)
	}
	return &c, nil
}

// SavePlaneCredential writes c to dir, readable by this user only: the token
// is a bearer secret.
func SavePlaneCredential(dir string, c PlaneCredential) error {
	if dir == "" {
		return errors.New("no directory to save the Control Plane credential to")
	}
	if c.URL == "" || c.Token == "" {
		return errors.New("a Control Plane credential needs both the URL and the token")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return replaceFile(filepath.Join(dir, PlaneCredentialFile), append(raw, '\n'), 0o600)
}
