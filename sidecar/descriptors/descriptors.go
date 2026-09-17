// Package descriptors resolves the entries of a grpc lane's `descriptors`
// list that are not files on disk.
//
// A descriptor set is the artifact a team's CI builds from its .proto files
// (protoc --include_imports --descriptor_set_out, buf build -o). Mounting it
// into the sidecar's filesystem works when one team owns the deployment;
// it stops working when the artifact is large — a Kubernetes ConfigMap caps
// at 1 MiB — or when several teams publish sets on their own cadence. An
// object store is the natural home for both, so a `descriptors` entry may be
// a URL and this package fetches it at startup.
//
// Fetchers register by URL scheme the way analyzer providers register by
// name, and for the same reason: a fetcher needs a cloud SDK or at least an
// OAuth2 library, and the root module carries libhoop and nothing else. Each
// fetcher is its own nested module (descriptors/gcs); the binary decides
// which schemes it can resolve by what it links, and the config decides
// which it uses. An entry naming a scheme the binary did not link is refused
// at config validation, never quietly treated as a file path.
package descriptors

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// Fetcher downloads one descriptor set. The URL has already been parsed and
// its scheme matched to the fetcher; the fetcher owns everything past that —
// credentials, the object-store API, size limits — and returns the raw
// FileDescriptorSet bytes for libhoop to index.
type Fetcher func(ctx context.Context, u *url.URL) ([]byte, error)

var (
	mu       sync.RWMutex
	fetchers = map[string]Fetcher{}
)

// Register makes a fetcher available for one URL scheme, matched
// case-insensitively. Call it from a package init. It panics on an empty or
// duplicate scheme: two modules claiming one scheme is a build mistake, and
// picking a winner would make behavior depend on import order.
func Register(scheme string, f Fetcher) {
	scheme = strings.ToLower(scheme)
	if scheme == "" {
		panic("sidecar/descriptors: Register called with an empty scheme")
	}
	if f == nil {
		panic("sidecar/descriptors: Register called with a nil fetcher")
	}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := fetchers[scheme]; dup {
		panic("sidecar/descriptors: duplicate fetcher registration for " + scheme)
	}
	fetchers[scheme] = f
}

// Registered lists the schemes linked into this binary, sorted so an error
// message is stable.
func Registered() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(fetchers))
	for s := range fetchers {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Scheme returns the lowercased URL scheme of a descriptors entry, or "" for
// a local path.
//
// Only `scheme://` counts (RFC 3986 scheme characters, then the authority
// marker). A bare colon does not, so a Windows path `C:\schemas\api.pb` and
// a relative `dir:with:colons/api.pb` stay files. The scheme is what the
// config validator reports on, so it is decided here, once, and Fetch parses
// the same way.
func Scheme(entry string) string {
	i := strings.Index(entry, "://")
	if i <= 0 {
		return ""
	}
	for j, r := range entry[:i] {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case j > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.'):
		default:
			return ""
		}
	}
	return strings.ToLower(entry[:i])
}

// Linked reports whether a fetcher for the scheme is registered.
func Linked(scheme string) bool {
	mu.RLock()
	defer mu.RUnlock()
	_, ok := fetchers[strings.ToLower(scheme)]
	return ok
}

// Fetch downloads the descriptor set a remote entry names. The error names
// what IS linked, because the common failure is a config asking for a
// scheme this build does not carry, and "unknown scheme gs" without that
// list sends an operator to the wrong file.
func Fetch(ctx context.Context, entry string) ([]byte, error) {
	scheme := Scheme(entry)
	if scheme == "" {
		return nil, fmt.Errorf("sidecar/descriptors: %q is not a URL", entry)
	}
	mu.RLock()
	f, ok := fetchers[scheme]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%s: no fetcher for scheme %q is linked into this binary (%s)",
			entry, scheme, describeLinked(Registered()))
	}
	u, err := url.Parse(entry)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", entry, err)
	}
	blob, err := f(ctx, u)
	if err != nil {
		// The URL is the source name; the caller prefixes the setting.
		return nil, fmt.Errorf("%s: %w", entry, err)
	}
	return blob, nil
}

func describeLinked(schemes []string) string {
	if len(schemes) == 0 {
		return "no fetcher is linked; build github.com/hoophq/hoop/sidecar/cmd"
	}
	return "linked: " + strings.Join(schemes, ", ")
}
