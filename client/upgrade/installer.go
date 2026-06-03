package upgrade

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// downloadTimeout is generous: tarballs are ~20-40MB but slow links and
// CDN warm-ups are possible.
const downloadTimeout = 5 * time.Minute

// Installer downloads, verifies and extracts hoop release artifacts into
// the layout's versions directory.
//
// Callers will typically use the package-level Install function. The
// Installer struct exists so tests can plug a different HTTP client or
// release base URL.
type Installer struct {
	Layout  Layout
	Client  *http.Client
	BaseURL string
}

// NewInstaller returns an Installer pointing at the public releases bucket.
func NewInstaller(l Layout) *Installer {
	return &Installer{
		Layout:  l,
		Client:  &http.Client{Timeout: downloadTimeout},
		BaseURL: ReleasesBaseURL,
	}
}

// Install downloads version for platform, verifies its sha256 against the
// release's checksums.txt, extracts the hoop binary into the version dir
// and returns the resulting store entry. It does NOT change the active
// symlink; callers do that explicitly to keep semantics separable
// (`hoop versions install` without `--use` should not switch).
//
// If the version is already on disk and not corrupted, Install is a no-op
// and returns the existing entry (after re-verifying that the binary
// still exists).
func (i *Installer) Install(version string, platform Platform, store *Store) (VersionEntry, error) {
	version = NormalizeVersion(version)
	if err := ValidateInstallableVersion(version); err != nil {
		return VersionEntry{}, err
	}

	if existing, ok := store.Get(version); ok {
		if _, err := os.Stat(i.Layout.VersionBinary(version)); err == nil {
			return existing, nil
		}
		// Recorded but binary missing: fall through and reinstall.
	}

	if err := i.Layout.EnsureDirs(); err != nil {
		return VersionEntry{}, err
	}

	artifactName := ArtifactName(version, platform)
	artifactURL := i.artifactURL(version, platform)
	checksumsURL := i.checksumsURL(version)

	expectedSHA, err := i.fetchExpectedChecksum(checksumsURL, artifactName)
	if err != nil {
		return VersionEntry{}, err
	}

	tmpDir, err := os.MkdirTemp(i.Layout.Home, "install-*")
	if err != nil {
		return VersionEntry{}, fmt.Errorf("failed creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	tarballPath := filepath.Join(tmpDir, artifactName)
	gotSHA, err := i.downloadFile(artifactURL, tarballPath)
	if err != nil {
		return VersionEntry{}, err
	}
	if !strings.EqualFold(gotSHA, expectedSHA) {
		return VersionEntry{}, fmt.Errorf(
			"checksum mismatch for %s\n  url=%s\n  expected=%s\n  actual=%s",
			artifactName, artifactURL, expectedSHA, gotSHA,
		)
	}

	extractedBinary := filepath.Join(tmpDir, platform.ExecutableName())
	if err := extractHoopBinary(tarballPath, extractedBinary, platform.ExecutableName()); err != nil {
		return VersionEntry{}, err
	}

	versionDir := i.Layout.VersionDir(version)
	if err := os.MkdirAll(versionDir, 0700); err != nil {
		return VersionEntry{}, fmt.Errorf("failed creating version dir %s: %w", versionDir, err)
	}
	finalBinary := i.Layout.VersionBinary(version)
	if err := os.Rename(extractedBinary, finalBinary); err != nil {
		return VersionEntry{}, fmt.Errorf("failed installing binary to %s: %w", finalBinary, err)
	}
	if err := os.Chmod(finalBinary, 0755); err != nil {
		return VersionEntry{}, fmt.Errorf("failed chmod binary %s: %w", finalBinary, err)
	}

	entry := VersionEntry{
		Version:     version,
		InstalledAt: time.Now().UTC(),
		Platform:    platform.String(),
		SHA256:      strings.ToLower(expectedSHA),
		SourceURL:   artifactURL,
	}
	store.Upsert(entry)
	if err := store.Save(i.Layout); err != nil {
		return VersionEntry{}, err
	}
	return entry, nil
}

func (i *Installer) artifactURL(version string, p Platform) string {
	return fmt.Sprintf("%s/%s/%s", i.BaseURL, version, ArtifactName(version, p))
}

func (i *Installer) checksumsURL(version string) string {
	return fmt.Sprintf("%s/%s/checksums.txt", i.BaseURL, version)
}

func (i *Installer) fetchExpectedChecksum(url, artifactName string) (string, error) {
	resp, err := i.Client.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed downloading checksums (%s): %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed downloading checksums (%s): status=%d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed reading checksums body: %w", err)
	}
	sum, ok := parseChecksums(string(body), artifactName)
	if !ok {
		return "", fmt.Errorf("checksum entry for %s not found in %s", artifactName, url)
	}
	return sum, nil
}

// parseChecksums parses GNU shasum-style content of the form
// "<sha256>  <filename>" (two spaces between fields) and returns the
// checksum for the requested file. The format matches what
// `find ... -name *_checksum.txt -exec cat ... > checksums.txt`
// produces in the release pipeline.
func parseChecksums(content, name string) (string, bool) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Trim a possible "*" prefix used by sha256sum binary mode.
		candidate := strings.TrimPrefix(fields[len(fields)-1], "*")
		if filepath.Base(candidate) == name {
			return strings.ToLower(fields[0]), true
		}
	}
	return "", false
}

func (i *Installer) downloadFile(url, dst string) (string, error) {
	resp, err := i.Client.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed downloading %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed downloading %s: status=%d", url, resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("failed creating %s: %w", dst, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		return "", fmt.Errorf("failed writing %s: %w", dst, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractHoopBinary extracts the entry whose basename equals wantName
// from a gzip+tar archive into dst. Any other entries are skipped.
//
// wantName is platform-dependent: the Windows release tarball carries
// hoop.exe while the Unix tarballs carry hoop (see Platform.ExecutableName),
// so callers pass the name for the platform they are installing.
func extractHoopBinary(tarball, dst, wantName string) error {
	f, err := os.Open(tarball)
	if err != nil {
		return fmt.Errorf("failed opening tarball %s: %w", tarball, err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("failed reading gzip from %s: %w", tarball, err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed reading tar entry: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(hdr.Name) != wantName {
			continue
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return fmt.Errorf("failed creating %s: %w", dst, err)
		}
		// SHA-256 of the tarball is already verified by the caller, so the
		// contents are trusted; no need to cap the size.
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return fmt.Errorf("failed extracting hoop binary: %w", err)
		}
		if err := out.Close(); err != nil {
			return fmt.Errorf("failed closing %s: %w", dst, err)
		}
		return nil
	}
	return fmt.Errorf("hoop binary (%s) not found inside %s", wantName, tarball)
}
