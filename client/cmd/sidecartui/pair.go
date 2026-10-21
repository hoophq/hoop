package sidecartui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// PairTimeout is how long a pairing waits for the browser. Signing up takes
// longer than a login, so it is longer than `hoop login`'s three minutes.
const PairTimeout = 15 * time.Minute

// pairing is one sign-in from the TUI: a localhost listener the web app's
// /sidecars/pair page sends the new sidecar's token to.
//
// The same shape as `hoop login`, with two differences. The port is any free
// one, because the web app page, not the gateway, builds the redirect. And a
// random state must come back with the token, so only the page this process
// opened can complete it.
type pairing struct {
	// PageURL is the web app page to open in the browser.
	PageURL string
	webURL  string
	state   string
	srv     *http.Server
	done    chan pairResult
	once    sync.Once
}

type pairResult struct {
	cred PlaneCredential
	err  error
}

// startPairing listens on 127.0.0.1 and builds the page URL for webURL, the
// Control Plane the sidecar pairs with (its web app and its API share it).
func startPairing(webURL, name string) (*pairing, error) {
	webURL = strings.TrimRight(webURL, "/")
	if _, err := url.ParseRequestURI(webURL); err != nil {
		return nil, fmt.Errorf("%q is not a web address: %w", webURL, err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("could not listen for the pairing on this computer: %w", err)
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		_ = ln.Close()
		return nil, err
	}
	p := &pairing{webURL: webURL, state: hex.EncodeToString(buf), done: make(chan pairResult, 1)}
	callback := fmt.Sprintf("http://%s/pair", ln.Addr().String())
	q := url.Values{"callback": {callback}, "state": {p.state}, "name": {name}}
	p.PageURL = webURL + "/sidecars/pair?" + q.Encode()

	mux := http.NewServeMux()
	mux.HandleFunc("/pair", p.handle)
	p.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

// handle takes the page's redirect: the state must match, then the token is
// kept and the browser goes on to the sidecar's page.
func (p *pairing) handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(p.state)) != 1 {
		http.Error(w, "this pairing was not started by this sidecar", http.StatusForbidden)
		return
	}
	token, id := strings.TrimSpace(q.Get("token")), strings.TrimSpace(q.Get("id"))
	if token == "" || id == "" {
		http.Error(w, "the pairing carried no sidecar token", http.StatusBadRequest)
		return
	}
	p.finish(pairResult{cred: PlaneCredential{URL: p.webURL, Token: token, ID: id, Name: q.Get("name")}})
	http.Redirect(w, r, p.webURL+"/sidecars/"+url.PathEscape(id), http.StatusFound)
}

func (p *pairing) finish(res pairResult) {
	p.once.Do(func() { p.done <- res })
}

// wait blocks until the browser pairs, ctx ends, or PairTimeout passes.
func (p *pairing) wait(ctx context.Context) (PlaneCredential, error) {
	ctx, cancel := context.WithTimeout(ctx, PairTimeout)
	defer cancel()
	select {
	case res := <-p.done:
		return res.cred, res.err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return PlaneCredential{}, errors.New("the pairing timed out; start it again")
		}
		return PlaneCredential{}, ctx.Err()
	}
}

// close stops the listener; the browser's redirect has been answered by then.
func (p *pairing) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.srv.Shutdown(ctx)
}

var notNameChar = regexp.MustCompile(`[^a-z0-9-]+`)

// pairName suggests the sidecar's name from the host: what the gateway
// accepts (letters, digits and dashes, at least three), editable on the page.
func pairName() string {
	host, _ := os.Hostname()
	host = strings.TrimSuffix(strings.ToLower(host), ".local")
	name := strings.Trim(notNameChar.ReplaceAllString(host, "-"), "-")
	if len(name) < 3 {
		return "my-sidecar"
	}
	if len(name) > 60 {
		name = strings.Trim(name[:60], "-")
	}
	return name
}
