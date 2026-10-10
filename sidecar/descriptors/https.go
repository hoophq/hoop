package descriptors

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// HTTPSScheme is the one scheme the root module resolves itself: a plain
// GET needs no SDK, so the fetcher lives here and every build has it. The
// package registers no plain http: the artifacts it fetches are a lane's
// schema and a codec plug-in's code, and either arriving over an
// unauthenticated transport is a supply-chain hole the sha256 check only
// half closes.
const HTTPSScheme = "https"

// MaxHTTPSBytes bounds one fetch. A descriptor set for a large API is a
// few megabytes and a wasm module a few more; the bound is there so a wrong
// URL (a release tarball on the same host) fails with a message before the
// process runs out of memory.
const MaxHTTPSBytes = 64 << 20

func init() {
	Register(HTTPSScheme, FetchHTTPS)
}

// FetchHTTPS downloads one https URL through client: a GET, any non-2xx
// status refused, the body bounded by MaxHTTPSBytes. Credentials are the
// client's business (a transport adding a header) or the URL's (a signed
// URL); the fetcher adds none.
func FetchHTTPS(ctx context.Context, u *url.URL, client *http.Client) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// The body of an error response is the server's explanation, and
		// a short slice of it tells an operator more than the status
		// alone.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("%s: %s", resp.Status, string(snippet))
	}
	// One byte past the bound tells a full read from a body that fills it.
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxHTTPSBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxHTTPSBytes {
		return nil, fmt.Errorf("the response exceeds %d bytes", MaxHTTPSBytes)
	}
	return body, nil
}
