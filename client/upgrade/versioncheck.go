package upgrade

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/hoophq/hoop/common/version"
)

// DisableVersionCheckEnv lets users silence the gateway/CLI version
// mismatch warning the same way they opt out of TLS verification.
const DisableVersionCheckEnv = "HOOP_DISABLE_VERSION_CHECK"

const serverHeaderPrefix = "hoopgateway/"

var (
	warnOnce       sync.Once
	warnSuppressed bool
	warnMu         sync.Mutex

	// localVersionFn is the test seam used to inject a deterministic
	// local CLI version. In production it just defers to common/version.
	localVersionFn = func() string { return version.Get().Version }
)

// resetVersionWarnStateForTests resets the once+suppression so tests can
// drive WarnOnceFromServerHeader repeatedly. Exposed via the _test.go file.
func resetVersionWarnStateForTests() {
	warnMu.Lock()
	defer warnMu.Unlock()
	warnOnce = sync.Once{}
	warnSuppressed = false
}

// SuppressVersionWarning disables the warning for the rest of the
// process. Useful in commands that already display version information
// (e.g. `hoop versions sync`) to avoid printing the warning right
// before the upgrade banner.
func SuppressVersionWarning() {
	warnMu.Lock()
	defer warnMu.Unlock()
	warnSuppressed = true
}

// WarnOnceFromServerHeader inspects the Server header from a gateway HTTP
// response (e.g. "hoopgateway/1.73.0") and prints a one-shot warning on
// stderr when the gateway version differs from the locally-built CLI.
//
// It is safe (and intended) to call from an HTTP middleware on every
// response: the actual warning is gated by sync.Once so callers only ever
// see a single line per process.
//
// The function is a no-op when:
//   - the env var HOOP_DISABLE_VERSION_CHECK is set to "true";
//   - SuppressVersionWarning has been called in this process;
//   - the header is empty or doesn't carry the "hoopgateway/" prefix;
//   - the local CLI version isn't a real build (empty / "unknown");
//   - the parsed gateway version equals the local one.
func WarnOnceFromServerHeader(header string) {
	warnOnceFromServerHeaderTo(os.Stderr, header)
}

// warnOnceFromServerHeaderTo is the test seam used by unit tests.
func warnOnceFromServerHeaderTo(w io.Writer, header string) {
	if os.Getenv(DisableVersionCheckEnv) == "true" {
		return
	}
	warnMu.Lock()
	suppressed := warnSuppressed
	warnMu.Unlock()
	if suppressed {
		return
	}

	gatewayVer, ok := parseServerHeader(header)
	if !ok {
		return
	}
	local := localVersionFn()
	if local == "" || local == "unknown" {
		return
	}
	if gatewayVer == local {
		return
	}
	warnOnce.Do(func() {
		fmt.Fprintf(w,
			"warn: hoop CLI version %s differs from gateway version %s; run `hoop versions sync` to match (set %s=true to silence)\n",
			local, gatewayVer, DisableVersionCheckEnv,
		)
	})
}

// parseServerHeader extracts the version from a header value of the form
// "hoopgateway/<version>". Returns ("", false) when the header is empty
// or doesn't carry the expected prefix.
func parseServerHeader(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(header, serverHeaderPrefix) {
		return "", false
	}
	v := strings.TrimSpace(strings.TrimPrefix(header, serverHeaderPrefix))
	if v == "" {
		return "", false
	}
	return v, true
}
