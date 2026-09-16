// Package gcs fetches a grpc lane's descriptor sets from Google Cloud
// Storage, for `descriptors` entries spelled gs://BUCKET/OBJECT.
//
// The read is one GET against the JSON API with alt=media — the object
// bytes, nothing else — authenticated with Application Default Credentials:
// Workload Identity on GKE, the attached service account on GCE or Cloud
// Run, GOOGLE_APPLICATION_CREDENTIALS, or gcloud on a laptop. The identity
// needs storage.objects.get on the object (roles/storage.objectViewer). There
// is no anonymous path: a public bucket reads fine with any valid token, and
// a lane that would decode payloads against a schema nobody authenticated
// for is not a configuration this package will build.
//
// `?generation=N` pins one version of the object, the JSON API's own
// parameter. Without it every startup reads the current version, which is
// the file-on-disk behavior an operator already has. A grpc lane binds its
// schema when its endpoint is built, so a new version of the object — like
// a new file on disk — is applied by a restart, not by a reload.
//
// It registers on import; a binary links it with a blank import, the way
// sidecar/cmd and `hoop start sidecar` do.
package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/hoophq/hoop/sidecar/descriptors"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// Scheme is the URL scheme this fetcher resolves.
const Scheme = "gs"

// scope is the narrowest OAuth scope that reads an object. It pairs with
// roles/storage.objectViewer on the bucket or object.
const scope = "https://www.googleapis.com/auth/devstorage.read_only"

// maxBytes bounds one object read. A descriptor set for a large API is a
// few megabytes; the bound is there so a wrong object name — a database
// dump in the same bucket — fails with a message instead of an OOM kill.
var maxBytes int64 = 512 << 20

// endpoint is the JSON API base. A variable so a test can stand in for it.
var endpoint = "https://storage.googleapis.com"

// findCredentials resolves ADC under the fetch's context: the token source
// it returns mints over that context's deadline, where the credential type
// allows one. A variable so a test can supply a token without a metadata
// server or a key file.
var findCredentials = func(ctx context.Context) (oauth2.TokenSource, error) {
	creds, err := google.FindDefaultCredentials(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("no application default credentials found "+
			"(Workload Identity, an attached service account, or GOOGLE_APPLICATION_CREDENTIALS): %w", err)
	}
	return creds.TokenSource, nil
}

// token is the last access token minted, reused while it is valid so the
// entries of one config share one mint. It is the TOKEN that is cached, not
// the source: an oauth2.TokenSource is bound to the context it was built
// with, and one built over an early fetch's deadline would fail every later
// mint with a timer that fired long ago, while one built over a context
// without a deadline could block a fetch for as long as the credential
// backend takes to answer. Each fetch therefore resolves its own source over
// its own context and refreshes only when this token has lapsed. A failed
// resolution leaves the field alone: the next fetch retries, because the
// usual cause is a Workload Identity binding that is still propagating.
var (
	tokenMu sync.Mutex
	token   *oauth2.Token
)

func init() {
	descriptors.Register(Scheme, Fetch)
}

// Fetch downloads one object. The URL was parsed by the descriptors package
// from a gs://BUCKET/OBJECT[?generation=N] entry.
func Fetch(ctx context.Context, u *url.URL) ([]byte, error) {
	bucket := u.Host
	object := strings.TrimPrefix(u.Path, "/")
	if bucket == "" || object == "" {
		return nil, errors.New("want gs://BUCKET/OBJECT")
	}
	if u.User != nil {
		return nil, errors.New("a gs:// URL carries no credentials; the fetch uses application default credentials")
	}
	if u.Fragment != "" {
		return nil, errors.New("a gs:// URL has no fragment; pin a version with ?generation=N")
	}
	// ParseQuery, not u.Query(): the latter drops a pair it cannot decode,
	// so `?generation=%zz` would read the current version while the
	// operator believes it pinned one. Anything but generation is refused
	// rather than dropped for the same reason: a typo such as ?generaton=N
	// must not silently unpin.
	params, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("malformed query %q: %w; the only parameter is ?generation=N", u.RawQuery, err)
	}
	query := url.Values{"alt": {"media"}}
	for key, values := range params {
		if key != "generation" || len(values) != 1 || !digits(values[0]) {
			return nil, fmt.Errorf("unsupported query %q; the only parameter is ?generation=N", key)
		}
		query.Set("generation", values[0])
	}

	tok, err := accessToken(ctx)
	if err != nil {
		return nil, err
	}

	reqURL := endpoint + "/storage/v1/b/" + url.PathEscape(bucket) + "/o/" + url.PathEscape(object) + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	tok.SetAuthHeader(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("object not found in bucket %q (%s)", bucket, resp.Status)
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("%s; the identity needs storage.objects.get on the object (roles/storage.objectViewer)", resp.Status)
	default:
		return nil, fmt.Errorf("storage.googleapis.com answered %s", resp.Status)
	}
	blob, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(blob)) > maxBytes {
		return nil, fmt.Errorf("object exceeds %d MiB; is this the descriptor set?", maxBytes>>20)
	}
	return blob, nil
}

// accessToken returns a valid token, minting one under ctx when the cached
// one has lapsed. The whole path — ADC discovery, the assertion exchange or
// the metadata call — runs under the fetch's deadline, so a credential
// backend that never answers is a fetch that fails, not a startup that
// hangs. The lock is held across the mint so concurrent lanes share one
// exchange rather than racing to mint the same token.
func accessToken(ctx context.Context) (*oauth2.Token, error) {
	tokenMu.Lock()
	defer tokenMu.Unlock()
	if token.Valid() {
		return token, nil
	}
	ts, err := findCredentials(ctx)
	if err != nil {
		return nil, err
	}
	tok, err := mint(ctx, ts)
	if err != nil {
		return nil, fmt.Errorf("could not mint a GCP access token (check the service account and the host clock): %w", err)
	}
	token = tok
	return tok, nil
}

// mint calls ts.Token() and gives up when ctx does. A source built from a
// key file or an external-account config already exchanges over ctx's HTTP
// client and deadline; the metadata-server source (GCE, Workload Identity)
// takes no context at all, and this select is what bounds it. On timeout the
// goroutine is left to finish on the metadata client's own dial timeout;
// its result is dropped, never cached.
func mint(ctx context.Context, ts oauth2.TokenSource) (*oauth2.Token, error) {
	type result struct {
		tok *oauth2.Token
		err error
	}
	done := make(chan result, 1)
	go func() {
		tok, err := ts.Token()
		done <- result{tok, err}
	}()
	select {
	case r := <-done:
		return r.tok, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
