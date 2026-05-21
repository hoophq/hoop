package upgrade

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/BurntSushi/toml"
)

// VersionEntry is a single installed version recorded in versions.toml.
type VersionEntry struct {
	Version     string    `toml:"version"`
	InstalledAt time.Time `toml:"installed_at"`
	Platform    string    `toml:"platform"`
	SHA256      string    `toml:"sha256"`
	SourceURL   string    `toml:"source_url"`
}

// Store is the on-disk state of the version manager. It is the source of
// truth for which versions are installed and which is active; the symlink
// at $HOME/.hoop/bin/hoop only mirrors it.
type Store struct {
	Active   string         `toml:"active"`
	Versions []VersionEntry `toml:"versions"`
}

// LoadStore reads versions.toml from the layout. A missing file is treated
// as an empty store (not an error) so first-run callers can just call Save.
func LoadStore(l Layout) (*Store, error) {
	data, err := os.ReadFile(l.StoreFile)
	if errors.Is(err, os.ErrNotExist) {
		return &Store{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed reading %s: %w", l.StoreFile, err)
	}
	var s Store
	if err := toml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("failed decoding %s: %w", l.StoreFile, err)
	}
	return &s, nil
}

// Save writes the store to disk with 0600 perms. It writes to a sibling
// temp file and renames so we never leave a half-written file behind.
func (s *Store) Save(l Layout) error {
	if err := l.EnsureDirs(); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(s); err != nil {
		return fmt.Errorf("failed encoding versions store: %w", err)
	}
	tmp, err := os.CreateTemp(l.Home, "versions-*.toml.tmp")
	if err != nil {
		return fmt.Errorf("failed creating temp store file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed writing temp store file: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed chmod temp store file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed closing temp store file: %w", err)
	}
	if err := os.Rename(tmpPath, l.StoreFile); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed renaming temp store file: %w", err)
	}
	return nil
}

// Has reports whether a version is recorded in the store.
func (s *Store) Has(version string) bool {
	_, ok := s.find(version)
	return ok
}

// Get returns the recorded entry for version, if present.
func (s *Store) Get(version string) (VersionEntry, bool) {
	return s.find(version)
}

func (s *Store) find(version string) (VersionEntry, bool) {
	for _, v := range s.Versions {
		if v.Version == version {
			return v, true
		}
	}
	return VersionEntry{}, false
}

// Upsert adds or replaces an entry by version.
func (s *Store) Upsert(entry VersionEntry) {
	for i, v := range s.Versions {
		if v.Version == entry.Version {
			s.Versions[i] = entry
			return
		}
	}
	s.Versions = append(s.Versions, entry)
}

// Remove deletes the entry for version; returns true if it was present.
// If the removed version was active, Active is cleared.
func (s *Store) Remove(version string) bool {
	for i, v := range s.Versions {
		if v.Version == version {
			s.Versions = append(s.Versions[:i], s.Versions[i+1:]...)
			if s.Active == version {
				s.Active = ""
			}
			return true
		}
	}
	return false
}

// Sorted returns the entries sorted by version string for stable listing.
// Note: this is lexical sort; it is good enough for UI listing because
// release versions tend to be zero-padded in practice.
func (s *Store) Sorted() []VersionEntry {
	out := make([]VersionEntry, len(s.Versions))
	copy(out, s.Versions)
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out
}
