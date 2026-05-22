// Package loginflow runs the daemon-owned OAuth flow used by
// hsh-tunneld.
//
// The protocol mirrors the one in client/cmd/login.go so the gateway
// side needs ZERO changes — same publicserverinfo / login URL / fixed
// callback address pattern. The differences are:
//
//   - The callback HTTP server runs inside the daemon (this package)
//     instead of in the hoop CLI process.
//   - On success we hand the token to a caller-supplied "persist"
//     callback rather than writing ~/.hoop/config.toml ourselves;
//     keeps loginflow free of filesystem assumptions and testable
//     without a real disk.
//   - The flow exposes a Start/Poll surface to the IPC layer
//     (tunnel/ipc) so the unprivileged `hsh` CLI can drive it without
//     ever touching the gateway directly.
//
// Concurrency model: at most ONE flow may be active at a time, because
// the gateway hardcodes the callback at 127.0.0.1:3587 (see
// pb.ClientLoginCallbackAddress). Start returns ErrFlowInProgress if a
// previous flow is still pending; the IPC layer translates that to a
// 409 Conflict so the UI can render "another login is already running,
// finish it or cancel".
package loginflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	pb "github.com/hoophq/hoop/common/proto"
)

// Status is the lifecycle of a single login attempt. It matches the
// ipc.LoginPollStatus values verbatim so the IPC layer can return our
// values straight through.
type Status string

const (
	// StatusPending means the callback server is up and we are still
	// waiting for the gateway to redirect the user's browser back to
	// /callback. UI should keep polling.
	StatusPending Status = "pending"

	// StatusDone means the gateway delivered a token AND the
	// OnSuccess callback completed cleanly (i.e. the token has been
	// persisted). UI should refresh /v1/status and stop polling.
	StatusDone Status = "done"

	// StatusError means either the callback delivered ?error=…, the
	// 3-minute timeout fired, the OnSuccess callback failed, or the
	// gateway could not provide a login URL. The associated message is
	// in attempt.errMessage and propagates to the UI via Poll.
	StatusError Status = "error"
)

// ErrFlowInProgress is returned by Start when a previous attempt is
// still StatusPending. Callers in the IPC layer should map this to
// HTTP 409 Conflict.
var ErrFlowInProgress = errors.New("loginflow: a login attempt is already in progress")

// Result is what Poll returns. Status is the current lifecycle state;
// Error is set only when Status == StatusError, and carries a
// human-readable explanation suitable for surfacing in the UI.
type Result struct {
	Status Status
	Error  string
}

// PersistFn is the callback invoked when the gateway redirects back
// with a usable token. The daemon implementation writes the token to
// daemonconfig and reconnects the netstack. If it returns an error,
// the flow transitions to StatusError and the operator sees the
// underlying message.
type PersistFn func(token string) error

// AuthMethodFn fetches the gateway's auth method (oidc / local /
// saml). The default implementation hits GET /api/publicserverinfo;
// tests inject a fake.
type AuthMethodFn func(ctx context.Context, apiURL string) (string, error)

// LoginURLFn fetches the gateway-provided login URL. The default
// implementation hits GET /api/login (or /api/saml/login for SAML);
// tests inject a fake.
type LoginURLFn func(ctx context.Context, apiURL, authMethod string) (string, error)

// Options configures Flow. APIURL and OnSuccess are required;
// everything else has sensible defaults.
type Options struct {
	// APIURL is the hoop gateway HTTPS base, e.g. "https://hoop.example.com".
	APIURL string

	// OnSuccess receives the token after a successful callback. It
	// runs inline (the callback HTTP handler blocks until it returns)
	// so the daemon can persist+reconnect before responding "all set"
	// to the browser tab.
	OnSuccess PersistFn

	// CallbackAddr overrides pb.ClientLoginCallbackAddress for tests.
	// Production code leaves this empty.
	CallbackAddr string

	// Timeout caps how long a single attempt may stay pending before
	// the flow transitions to StatusError("timeout"). Defaults to 3
	// minutes, matching client/cmd/login.go.
	Timeout time.Duration

	// HTTPClient is used for the gateway-side calls
	// (/api/publicserverinfo, /api/login). Tests inject a fake;
	// production gets a sensible default.
	HTTPClient *http.Client

	// AuthMethod overrides the gateway auth-method fetcher. Tests use
	// this to bypass /api/publicserverinfo entirely.
	AuthMethod AuthMethodFn

	// LoginURL overrides the gateway login-URL fetcher. Same purpose.
	LoginURL LoginURLFn
}

// Flow is the one-attempt-at-a-time state machine. Construct with
// New(); kick off attempts with Start; check status with Poll.
//
// All exported methods are safe for concurrent use.
type Flow struct {
	opts Options

	mu       sync.Mutex
	current  *attempt           // nil means "no active attempt"
	finished map[string]*Result // state → terminal result, kept for late Poll calls
}

// attempt holds the runtime state of one in-flight login attempt.
// Once it transitions to a terminal state (Done or Error) it is moved
// into Flow.finished and Flow.current is cleared so a new attempt may
// start.
type attempt struct {
	state    string             // opaque token returned to the UI; identifies this attempt
	loginURL string             // browser URL the user must open
	server   *http.Server       // callback listener
	cancel   context.CancelFunc

	// shutdownDone closes once the callback server has fully released
	// its port. Callers that need to re-bind the same address
	// immediately (e.g. an explicit Cancel followed by a fresh Start)
	// wait on this channel to avoid EADDRINUSE.
	shutdownDone chan struct{}

	mu         sync.Mutex
	status     Status
	errMessage string
}

// New returns a Flow ready to accept Start calls. It does not bind any
// network listener or dial the gateway — that happens at Start time.
func New(opts Options) (*Flow, error) {
	if opts.APIURL == "" {
		return nil, errors.New("loginflow.New: APIURL is required")
	}
	if opts.OnSuccess == nil {
		return nil, errors.New("loginflow.New: OnSuccess is required")
	}
	if opts.CallbackAddr == "" {
		opts.CallbackAddr = pb.ClientLoginCallbackAddress
	}
	if opts.Timeout == 0 {
		opts.Timeout = 3 * time.Minute
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if opts.AuthMethod == nil {
		opts.AuthMethod = defaultFetchAuthMethod(opts.HTTPClient)
	}
	if opts.LoginURL == nil {
		opts.LoginURL = defaultFetchLoginURL(opts.HTTPClient)
	}
	return &Flow{
		opts:     opts,
		finished: make(map[string]*Result),
	}, nil
}

// CallbackAddr returns the address the flow's callback server listens
// on. Exposed for tests and for the daemon's startup log line so
// operators can see where the OAuth redirect will land.
func (f *Flow) CallbackAddr() string { return f.opts.CallbackAddr }

// Start initiates a new attempt. It:
//
//  1. Fetches the gateway's auth method via /api/publicserverinfo.
//  2. Fetches the login URL via /api/login (or /api/saml/login).
//  3. Binds the callback server on opts.CallbackAddr.
//  4. Generates an opaque state token.
//  5. Spawns the timeout goroutine.
//
// Returns (loginURL, state, nil) on success. Returns
// ErrFlowInProgress if a previous attempt is still pending. Any other
// error means we couldn't even reach the gateway — caller surfaces it
// to the user.
func (f *Flow) Start(ctx context.Context) (loginURL string, state string, err error) {
	f.mu.Lock()
	if f.current != nil {
		f.mu.Unlock()
		return "", "", ErrFlowInProgress
	}
	// Reserve a slot eagerly so a concurrent Start sees us as in-flight
	// even before the gateway calls succeed. We unwind it on error.
	state, err = randomState()
	if err != nil {
		f.mu.Unlock()
		return "", "", fmt.Errorf("loginflow: generate state: %w", err)
	}
	att := &attempt{
		state:        state,
		status:       StatusPending,
		shutdownDone: make(chan struct{}),
	}
	f.current = att
	f.mu.Unlock()

	cleanupOnError := func() {
		f.mu.Lock()
		if f.current == att {
			f.current = nil
		}
		f.mu.Unlock()
	}

	authMethod, err := f.opts.AuthMethod(ctx, f.opts.APIURL)
	if err != nil {
		cleanupOnError()
		return "", "", fmt.Errorf("fetch auth method: %w", err)
	}
	url, err := f.opts.LoginURL(ctx, f.opts.APIURL, authMethod)
	if err != nil {
		cleanupOnError()
		return "", "", fmt.Errorf("fetch login URL: %w", err)
	}
	att.loginURL = url

	if err := f.bindCallback(att); err != nil {
		cleanupOnError()
		return "", "", fmt.Errorf("bind callback: %w", err)
	}

	// Spawn the timeout watcher. The watcher takes ownership of
	// cancelling the attempt if the user never completes the browser
	// flow.
	attemptCtx, cancel := context.WithTimeout(context.Background(), f.opts.Timeout)
	att.cancel = cancel
	go f.watchTimeout(attemptCtx, att)

	return url, state, nil
}

// Poll reports the current state of the attempt identified by state.
// Returns (Result, true) if the state is known, (zero, false) if the
// state has never existed (typo, restart, etc.).
//
// Polling a terminal attempt is allowed and idempotent: terminal
// results are retained in f.finished long enough for the UI to
// observe them. The retention is unbounded in this version because
// the UI polls quickly and a daemon restart wipes the slate anyway.
// If we ever need GC, it would key off a TTL on terminal results.
func (f *Flow) Poll(state string) (Result, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.current != nil && f.current.state == state {
		f.current.mu.Lock()
		defer f.current.mu.Unlock()
		return Result{Status: f.current.status, Error: f.current.errMessage}, true
	}
	if r, ok := f.finished[state]; ok {
		return *r, true
	}
	return Result{}, false
}

// Cancel aborts any in-flight attempt. Blocks until the callback
// listener has fully released its port so callers can immediately
// re-Start (e.g. retry after a typo). Idempotent — a Flow with no
// active attempt returns immediately.
func (f *Flow) Cancel() {
	f.mu.Lock()
	att := f.current
	f.mu.Unlock()
	if att == nil {
		return
	}
	f.finishAttempt(att, StatusError, "login cancelled")
	<-att.shutdownDone
}

// ----------------------------------------------------------------------
// internals
// ----------------------------------------------------------------------

// bindCallback starts the HTTP server that the gateway will redirect
// the user's browser to. It returns once the listener is up.
func (f *Flow) bindCallback(att *attempt) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		errParam := r.URL.Query().Get("error")
		token := r.URL.Query().Get("token")

		if errParam != "" {
			_, _ = io.WriteString(w, "Login failed: "+errParam)
			f.finishAttempt(att, StatusError, errParam)
			return
		}
		if token == "" {
			const msg = "callback delivered without a token"
			_, _ = io.WriteString(w, msg)
			f.finishAttempt(att, StatusError, msg)
			return
		}

		if err := f.opts.OnSuccess(token); err != nil {
			// Tell the browser AND the polling client both. The
			// browser tab sees a useful error rather than a generic
			// "OK" while the daemon-side persist failed.
			_, _ = io.WriteString(w, "Login received but the daemon failed to persist it: "+err.Error())
			f.finishAttempt(att, StatusError, "persist token: "+err.Error())
			return
		}

		_, _ = io.WriteString(w, defaultSuccessHTML)
		f.finishAttempt(att, StatusDone, "")
	})

	srv := &http.Server{
		Addr:              f.opts.CallbackAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	att.server = srv

	// Listen synchronously so a bind error is reported back to Start.
	// We close the listener as part of finishAttempt, never here.
	ln, err := defaultListen("tcp", f.opts.CallbackAddr)
	if err != nil {
		return err
	}
	go func() {
		// Squash ErrServerClosed — that's the expected path when we
		// Shut down after a successful callback.
		_ = srv.Serve(ln)
	}()
	return nil
}

// watchTimeout transitions the attempt to StatusError("timeout") if
// the user never completes the flow within Options.Timeout. If the
// attempt has already transitioned to Done or Error by other means,
// the watcher silently exits — finishAttempt is idempotent.
func (f *Flow) watchTimeout(ctx context.Context, att *attempt) {
	<-ctx.Done()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		f.finishAttempt(att, StatusError, fmt.Sprintf("login timed out after %s", f.opts.Timeout))
	}
}

// finishAttempt atomically transitions an attempt from Pending to a
// terminal state, shuts down its callback server, and clears the
// flow's "current" slot so a fresh Start can succeed.
//
// Idempotent: calling on an already-terminal attempt is a no-op.
func (f *Flow) finishAttempt(att *attempt, status Status, msg string) {
	att.mu.Lock()
	if att.status != StatusPending {
		att.mu.Unlock()
		return
	}
	att.status = status
	att.errMessage = msg
	att.mu.Unlock()

	// Cancel the timeout goroutine; on success it would have fired
	// anyway, but we want it gone immediately so it doesn't race the
	// next Start.
	if att.cancel != nil {
		att.cancel()
	}

	// Shut down the callback server. This MUST run on a goroutine:
	// finishAttempt is called from inside the /callback HTTP handler
	// on success, and http.Server.Shutdown blocks until all in-flight
	// requests return — so a synchronous shutdown deadlocks on the
	// very handler that triggered it. Off-loading to a goroutine lets
	// the handler return, which lets Shutdown observe the connection
	// going idle, which lets it exit cleanly.
	//
	// shutdownDone closes once the port is fully released, so callers
	// that immediately want to re-bind (Cancel + fresh Start) can wait
	// without racing the kernel's socket cleanup.
	if att.server != nil {
		go func(srv *http.Server, done chan struct{}) {
			defer close(done)
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		}(att.server, att.shutdownDone)
	} else {
		// No server (e.g. attempt aborted before bindCallback ran):
		// signal "shutdown" immediately so Cancel can return.
		close(att.shutdownDone)
	}

	// Move the attempt out of current and remember its result.
	f.mu.Lock()
	if f.current == att {
		f.current = nil
	}
	f.finished[att.state] = &Result{Status: status, Error: msg}
	f.mu.Unlock()
}

// ----------------------------------------------------------------------
// gateway helpers (defaults; tests inject custom)
// ----------------------------------------------------------------------

// defaultFetchAuthMethod calls GET <apiURL>/api/publicserverinfo and
// returns the `auth_method` field. Matches client/cmd/login.go's
// fetchAuthMethod verbatim so we can rely on the gateway behaving the
// same as it does for the hoop CLI.
func defaultFetchAuthMethod(c *http.Client) AuthMethodFn {
	return func(ctx context.Context, apiURL string) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/api/publicserverinfo", nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return "", fmt.Errorf("GET /api/publicserverinfo: status=%d, body=%q", resp.StatusCode, string(body))
		}
		var info map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			return "", fmt.Errorf("decode publicserverinfo: %w", err)
		}
		v, _ := info["auth_method"].(string)
		if v == "" {
			return "", errors.New("publicserverinfo did not return auth_method")
		}
		return v, nil
	}
}

// defaultFetchLoginURL calls GET <apiURL>/api/login (or /api/saml/login
// for SAML) and returns the `login_url` field. Matches
// client/cmd/login.go's requestForUrl.
func defaultFetchLoginURL(c *http.Client) LoginURLFn {
	return func(ctx context.Context, apiURL, authMethod string) (string, error) {
		path := "/api/login"
		if authMethod == "saml" {
			path = "/api/saml/login"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+path, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return "", fmt.Errorf("GET %s: status=%d, body=%q", path, resp.StatusCode, string(body))
		}
		var body struct {
			LoginURL string `json:"login_url"`
			Message  string `json:"message"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return "", fmt.Errorf("decode %s: %w", path, err)
		}
		if body.LoginURL == "" {
			if body.Message != "" {
				return "", fmt.Errorf("gateway: %s", body.Message)
			}
			return "", errors.New("gateway did not return a login_url")
		}
		return body.LoginURL, nil
	}
}

// randomState produces a 32-byte hex token used as the opaque state
// identifier passed to the IPC layer. Long enough to be unguessable;
// short enough to fit in a query param without controversy.
func randomState() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// defaultSuccessHTML is what the browser tab shows after a successful
// callback. Intentionally minimal — fancier UX (logo, "you can close
// this tab" cue) can be templated in later without touching the
// state machine.
const defaultSuccessHTML = `<!DOCTYPE html>
<html><head><title>Hoop Tunnel — Signed in</title></head>
<body style="font-family: -apple-system, system-ui, sans-serif; max-width: 480px; margin: 4em auto; text-align: center;">
<h1>✓ Signed in</h1>
<p>You can close this tab and return to your terminal.</p>
</body></html>`
