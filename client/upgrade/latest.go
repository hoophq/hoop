package upgrade

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LatestVersionURL is the canonical pointer to the most recent hoop
// release. The file is a single plain-text line, e.g. "1.73.0\n".
// It is updated by the release pipeline together with the tarball and
// checksum artifacts under https://releases.hoop.dev/release/.
const LatestVersionURL = "https://releases.hoop.dev/release/latest.txt"

// latestFetchTimeout caps how long FetchLatestVersion waits before
// giving up. The latest.txt response is a handful of bytes so even a
// slow link should respond well under this budget.
const latestFetchTimeout = 10 * time.Second

// latestResponseLimit caps how many bytes we read from latest.txt. A
// real response is ~10 bytes; anything wildly larger indicates either
// a misconfigured CDN or a hostile origin and is rejected.
const latestResponseLimit = 64

// FetchLatestVersion returns the latest hoop release version as
// published on releases.hoop.dev. The returned value is normalized
// (no leading "v") and validated against ValidateInstallableVersion so
// callers can hand it straight to the installer.
func FetchLatestVersion() (string, error) {
	return fetchLatestVersionFrom(LatestVersionURL, nil)
}

// fetchLatestVersionFrom is the test seam used by latest_test.go to
// point at an httptest server. Passing client=nil yields a fresh
// http.Client with the standard timeout; tests inject a custom client
// so they don't depend on the package-level constants.
func fetchLatestVersionFrom(url string, client *http.Client) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: latestFetchTimeout}
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed building request for %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach %s: %w\n  - check your network/DNS\n  - verify https://releases.hoop.dev is reachable from this machine", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected response from %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, latestResponseLimit+1))
	if err != nil {
		return "", fmt.Errorf("failed reading response from %s: %w", url, err)
	}
	if len(body) > latestResponseLimit {
		return "", fmt.Errorf("response from %s was unexpectedly large (>%d bytes); refusing to trust it", url, latestResponseLimit)
	}
	ver := NormalizeVersion(strings.TrimSpace(string(body)))
	if ver == "" {
		return "", fmt.Errorf("response from %s was empty", url)
	}
	if err := ValidateInstallableVersion(ver); err != nil {
		return "", fmt.Errorf("latest version published at %s (%q) is not installable by this CLI: %w", url, ver, err)
	}
	return ver, nil
}
