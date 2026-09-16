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
// parameter. Without it every startup and reload reads the current version,
// which is the file-on-disk behavior an operator already has.
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

// findCredentials resolves ADC. A variable so a test can supply a token
// without a metadata server or a key file.
var findCredentials = func(ctx context.Context) (oauth2.TokenSource, error) {
	creds, err := google.FindDefaultCredentials(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("no application default credentials found "+
			"(Workload Identity, an attached service account, or GOOGLE_APPLICATION_CREDENTIALS): %w", err)
	}
	return creds.TokenSource, nil
}

// tokenSrc caches the resolved credential. oauth2.TokenSource refreshes
// before expiry, so one source serves every entry in a config and every
// reload without minting a token per fetch. A failed resolution is not
// cached: the next reload retries, because the usual cause is a Workload
// Identity binding that is still propagating.
var (
	tokenMu  sync.Mutex
	tokenSrc oauth2.TokenSource
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
	query := url.Values{"alt": {"media"}}
	for key, values := range u.Query() {
		// Anything but generation is refused rather than dropped: a typo
		// such as ?generaton=N would otherwise read the current version
		// while the operator believes it pinned one.
		if key != "generation" || len(values) != 1 || !digits(values[0]) {
			return nil, fmt.Errorf("unsupported query %q; the only parameter is ?generation=N", key)
		}
		query.Set("generation", values[0])
	}

	ts, err := tokenSource(ctx)
	if err != nil {
		return nil, err
	}
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("could not mint a GCP access token (check the service account and the host clock): %w", err)
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

func tokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	tokenMu.Lock()
	defer tokenMu.Unlock()
	if tokenSrc != nil {
		return tokenSrc, nil
	}
	// The source outlives this fetch and refreshes tokens over the context
	// it was built with, so it must not inherit the fetch's deadline: a
	// reload minutes later would otherwise fail with "context deadline
	// exceeded" from a timer that fired at startup.
	ts, err := findCredentials(context.WithoutCancel(ctx))
	if err != nil {
		return nil, err
	}
	tokenSrc = ts
	return ts, nil
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
