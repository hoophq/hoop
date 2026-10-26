package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hoophq/hoop/client/cmd/sidecartui"
	"github.com/hoophq/hoop/common/clientconfig"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// A sidecar with no token logs in the way a TV does: it makes its own token,
// shows a URL and a short code, and waits while a person approves the code in
// the web app. Approving creates the sidecar in that person's organization
// with the hash of this token, so the token never comes back over the wire.
// With no control plane named, the plane is hoop's hosted one, where a new
// organization runs the free tier.

// DefaultSidecarControlPlaneURL is the plane a sidecar logs in to when
// HOOP_CONTROL_PLANE_URL names none.
const DefaultSidecarControlPlaneURL = clientconfig.SaaSWebURL

// sidecarLoginFile holds the sidecar's logins, one per control plane, in the
// state folder (sidecarStateDir). Tokens are bearer secrets: 0600.
const sidecarLoginFile = "credentials.json"

// SidecarStateDirEnv moves the state folder, so a container keeps its login
// on a volume instead of a home directory it loses on restart.
const SidecarStateDirEnv = "HOOP_SIDECAR_STATE_DIR"

// savedSidecarLogin is the credential a login produced. Page is the
// sidecar's page in the web app.
type savedSidecarLogin struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	Name  string `json:"name"`
	Page  string `json:"page,omitempty"`
	// Config is the file this sidecar last booted, so the next bare run
	// boots it again.
	Config string `json:"config,omitempty"`
}

// sidecarCredentials is the file: logins keyed by their plane's URL. A
// token is only ever read back for the plane that issued it.
type sidecarCredentials struct {
	Planes map[string]savedSidecarLogin `json:"planes"`
}

// sidecarStateDir is where the sidecar keeps its logins: the env var, else
// ~/.hoop/sidecar. Empty when neither is known.
func sidecarStateDir() string {
	if dir := os.Getenv(SidecarStateDirEnv); dir != "" {
		return dir
	}
	return sidecarSaveDir()
}

func readSidecarCredentials(dir string) (sidecarCredentials, error) {
	creds := sidecarCredentials{Planes: map[string]savedSidecarLogin{}}
	if dir == "" {
		return creds, nil
	}
	path := filepath.Join(dir, sidecarLoginFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return creds, nil
	}
	if err != nil {
		return creds, err
	}
	if err := json.Unmarshal(raw, &creds); err != nil {
		return creds, fmt.Errorf("%s is not a sidecar credentials file (%v); "+
			"run `hoop sidecar logout --all` and log in again", path, err)
	}
	if creds.Planes == nil {
		creds.Planes = map[string]savedSidecarLogin{}
	}
	return creds, nil
}

// loadSidecarLogin returns the login saved for planeURL, or nil. A login for
// another plane is not this one's: the token would be sent to a host that
// did not issue it.
func loadSidecarLogin(dir, planeURL string) (*savedSidecarLogin, error) {
	creds, err := readSidecarCredentials(dir)
	if err != nil {
		return nil, err
	}
	s, ok := creds.Planes[normalizePlaneURL(planeURL)]
	if !ok || s.Token == "" {
		return nil, nil
	}
	return &s, nil
}

// onlySidecarLogin returns the login saved here when there is exactly one,
// else nil: with two, which plane to use is the person's choice.
func onlySidecarLogin(dir string) (*savedSidecarLogin, error) {
	logins, err := listSidecarLogins(dir)
	if err != nil || len(logins) != 1 || logins[0].Token == "" {
		return nil, err
	}
	return &logins[0], nil
}

// listSidecarLogins returns every saved login, for status.
func listSidecarLogins(dir string) ([]savedSidecarLogin, error) {
	creds, err := readSidecarCredentials(dir)
	if err != nil {
		return nil, err
	}
	out := make([]savedSidecarLogin, 0, len(creds.Planes))
	for _, s := range creds.Planes {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out, nil
}

// saveSidecarLogin records s for its plane, keeping the other planes'.
func saveSidecarLogin(dir string, s savedSidecarLogin) (string, error) {
	creds, err := readSidecarCredentials(dir)
	if err != nil {
		return "", err
	}
	s.URL = normalizePlaneURL(s.URL)
	creds.Planes[s.URL] = s
	return writeSidecarCredentials(dir, creds)
}

// removeSidecarLogin forgets planeURL's login, or every login when planeURL
// is empty. It reports whether anything was removed.
func removeSidecarLogin(dir, planeURL string) (bool, error) {
	creds, err := readSidecarCredentials(dir)
	if err != nil && planeURL != "" {
		return false, err
	}
	if planeURL == "" {
		if dir == "" {
			return false, nil
		}
		err := os.Remove(filepath.Join(dir, sidecarLoginFile))
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return err == nil, err
	}
	key := normalizePlaneURL(planeURL)
	if _, ok := creds.Planes[key]; !ok {
		return false, nil
	}
	delete(creds.Planes, key)
	_, err = writeSidecarCredentials(dir, creds)
	return err == nil, err
}

func writeSidecarCredentials(dir string, creds sidecarCredentials) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("no folder for the sidecar login: set %s or HOME", SidecarStateDirEnv)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, sidecarLoginFile)
	// Write then rename, so a crash never leaves half a token behind, and
	// chmod the temp file before the token is in it.
	tmp, err := os.CreateTemp(dir, sidecarLoginFile+".*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}

func normalizePlaneURL(raw string) string { return strings.TrimRight(strings.TrimSpace(raw), "/") }

// newSidecarToken makes a token in the format the plane issues
// (services.GenerateSidecarKey): "hsc_" and 32 random bytes.
func newSidecarToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "hsc_" + base64.RawURLEncoding.EncodeToString(b), nil
}

var sidecarNameUnsafe = regexp.MustCompile(`[^a-z0-9-]+`)

// defaultSidecarName is the name a login asks for: this host's name in the
// plane's resource-name alphabet. The approving admin can change it. A host
// name the system cannot give is said on out, and the name is "sidecar".
func defaultSidecarName(out io.Writer) string {
	host, err := os.Hostname()
	if err != nil {
		fmt.Fprintf(out, "Could not read this machine's name (%v); the sidecar asks to be named \"sidecar\". "+
			"Rename it when you approve it.\n", err)
	}
	return sidecarNameFromHost(host)
}

func sidecarNameFromHost(host string) string {
	if i := strings.IndexByte(host, '.'); i > 0 {
		host = host[:i]
	}
	name := strings.Trim(sidecarNameUnsafe.ReplaceAllString(strings.ToLower(host), "-"), "-")
	if len(name) > 48 {
		name = strings.Trim(name[:48], "-")
	}
	// The plane wants at least 3 characters.
	if len(name) < 3 {
		return "sidecar"
	}
	return name
}

type sidecarLoginStart struct {
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type sidecarLoginPoll struct {
	Status string `json:"status"`
	Name   string `json:"name"`
	URL    string `json:"url"`
}

// sidecarLoginer runs one device-code login. Fields are seams for tests.
type sidecarLoginer struct {
	planeURL string
	name     string
	out      io.Writer
	client   *http.Client
	open     func(string) error
	// minInterval floors the plane's poll interval; tests lower it.
	minInterval time.Duration
	// events, when set, gets the login as JSON lines (sidecarLoginEvent) for
	// a reader that is not a person: a marketplace console, an agent.
	events io.Writer
}

// sidecarLoginEvent is one machine-readable step of a login, one JSON line.
// Event is sidecar_login_required (with the link and the code a person
// approves) or sidecar_login_approved.
type sidecarLoginEvent struct {
	Time            string `json:"time"`
	Level           string `json:"level"`
	Msg             string `json:"msg"`
	Event           string `json:"event"`
	Plane           string `json:"control_plane_url"`
	VerificationURL string `json:"verification_url,omitempty"`
	UserCode        string `json:"user_code,omitempty"`
	ExpiresIn       int    `json:"expires_in,omitempty"`
	Sidecar         string `json:"sidecar,omitempty"`
}

func (l *sidecarLoginer) emit(ev sidecarLoginEvent) {
	if l.events == nil {
		return
	}
	ev.Time, ev.Level = time.Now().UTC().Format(time.RFC3339), "INFO"
	raw, _ := json.Marshal(ev)
	fmt.Fprintln(l.events, string(raw))
}

// loginTicket is a started login: what the person approves, and the token
// the approval binds to this sidecar.
type loginTicket struct {
	planeURL string
	token    string
	start    sidecarLoginStart
}

// run logs in and returns the credential, printing the link for a person at
// a plain terminal. It blocks until the code is approved, denied or expired,
// or ctx ends.
func (l *sidecarLoginer) run(ctx context.Context) (*savedSidecarLogin, error) {
	t, err := l.begin(ctx)
	if err != nil {
		return nil, err
	}
	l.emit(sidecarLoginEvent{Msg: "sidecar login required: approve the code in the web app",
		Event: "sidecar_login_required", Plane: t.planeURL, VerificationURL: t.start.VerificationURL,
		UserCode: t.start.UserCode, ExpiresIn: t.start.ExpiresIn})
	// The link carries the code, so nothing is typed. The code is shown so the
	// person can check that the page they approve is this sidecar's.
	fmt.Fprintf(l.out, "\nTo connect this sidecar, approve it in your browser:\n\n  %s\n\n"+
		"The page shows the code %s. Approve only if it matches this one.\n\n",
		t.start.VerificationURL, t.start.UserCode)
	opened := l.open != nil && l.open(t.start.VerificationURL) == nil
	if opened {
		fmt.Fprintln(l.out, "Opened your browser. Waiting for approval...")
	} else {
		fmt.Fprintln(l.out, "Open the link above. Waiting for approval...")
	}
	login, err := l.wait(ctx, t)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(l.out, "Approved: this sidecar is %q at %s\n", login.Name, login.URL)
	l.emit(sidecarLoginEvent{Msg: "sidecar login approved", Event: "sidecar_login_approved",
		Plane: login.URL, Sidecar: login.Name})
	return login, nil
}

// begin makes this sidecar's token and asks the plane for a code.
func (l *sidecarLoginer) begin(ctx context.Context) (*loginTicket, error) {
	planeURL := normalizePlaneURL(l.planeURL)
	if err := sidecartui.PlaneTransportError(planeURL); err != nil {
		return nil, err
	}
	token, err := newSidecarToken()
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]string{"name": l.name})
	t := &loginTicket{planeURL: planeURL, token: token}
	status, raw, err := l.post(ctx, planeURL+"/api/sidecars/login", token, body, &t.start)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusNotFound:
		return nil, fmt.Errorf("the control plane at %s does not support sidecar login yet; "+
			"set %s to a control plane that does, or register the sidecar in its web app and pass --token",
			planeURL, daemon.ControlPlaneURLEnv)
	case status != http.StatusCreated:
		return nil, fmt.Errorf("the control plane at %s refused the sidecar login (%d): %s",
			planeURL, status, planeMessage(raw))
	}
	return t, nil
}

// wait polls until the code is approved, denied or expired, or ctx ends.
func (l *sidecarLoginer) wait(ctx context.Context, t *loginTicket) (*savedSidecarLogin, error) {
	interval := time.Duration(t.start.Interval) * time.Second
	if interval < l.minInterval {
		interval = l.minInterval
	}
	deadline := time.Now().Add(time.Duration(t.start.ExpiresIn)*time.Second + interval)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		var poll sidecarLoginPoll
		status, raw, err := l.post(ctx, t.planeURL+"/api/sidecars/login/poll", t.token, nil, &poll)
		if err != nil {
			// A dropped request is not an answer: keep waiting until the
			// code itself expires.
			if time.Now().After(deadline) {
				return nil, err
			}
			continue
		}
		switch status {
		case http.StatusOK:
			return &savedSidecarLogin{URL: t.planeURL, Token: t.token, Name: poll.Name, Page: poll.URL}, nil
		case http.StatusPreconditionRequired:
			if time.Now().After(deadline) {
				return nil, errors.New("the sidecar login code expired; run the command again")
			}
		case http.StatusForbidden:
			return nil, errors.New("the sidecar login was denied in the web app")
		case http.StatusGone:
			return nil, errors.New("the sidecar login code expired; run the command again")
		default:
			return nil, fmt.Errorf("the control plane at %s answered the sidecar login poll with %d: %s",
				t.planeURL, status, planeMessage(raw))
		}
	}
}

// post sends the token in the sidecar header and decodes a JSON answer.
func (l *sidecarLoginer) post(ctx context.Context, url, token string, body []byte, into any) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("hoop-sidecar-token", token)
	resp, err := l.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("the control plane at %s is unreachable: %w", l.planeURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return 0, nil, fmt.Errorf("failed reading the answer of the control plane at %s: %w", l.planeURL, err)
	}
	// A success must decode: empty fields would read as a login. An error
	// answer is decoded when it can be; its message comes from raw.
	if err := json.Unmarshal(raw, into); err != nil && resp.StatusCode/100 == 2 {
		return 0, nil, fmt.Errorf("the control plane at %s answered %d with a body that is not a sidecar login: %w",
			l.planeURL, resp.StatusCode, err)
	}
	return resp.StatusCode, raw, nil
}

func planeMessage(raw []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &m) == nil && m.Message != "" {
		return m.Message
	}
	return strings.TrimSpace(string(raw))
}

// sidecarLoginHTTPClient never follows a redirect, for the reason the
// handshake's client does not: the token rides a custom header Go would
// forward to whoever answered the Location.
func sidecarLoginHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// sidecarCredentialGiven is whether this run already names a credential:
// the token flag, its env var, or a platform identity.
func sidecarCredentialGiven() bool {
	return sidecarTokenGiven() || os.Getenv(daemon.SidecarIdentityTypeEnv) != ""
}

func sidecarTokenGiven() bool {
	return sidecarTokenFlag != "" || os.Getenv(daemon.SidecarTokenEnv) != ""
}

// waitForPlaneConfig runs setup until the plane holds a listener for this
// sidecar. A sidecar that just logged in is empty, and with no config file
// to import there is nothing to serve until someone adds a listener in the
// web app; waiting beats exiting with a 412 the person has to rerun past.
func waitForPlaneConfig(ctx context.Context, out io.Writer, login *savedSidecarLogin, setup func() error) error {
	err := setup()
	if !errors.Is(err, daemon.ErrPlaneHasNoConfig) {
		return err
	}
	// A plane older than the page link answers without one; its sidecar
	// list is where the web app is served on every hosted plane.
	page, name := login.Page, login.Name
	if page == "" {
		page = normalizePlaneURL(login.URL) + "/sidecars"
	}
	fmt.Fprintf(out, "\nSidecar %q has no listeners yet. Add one in the web app:\n\n  %s\n\nWaiting for its configuration...\n", name, page)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
		if err = setup(); !errors.Is(err, daemon.ErrPlaneHasNoConfig) {
			return err
		}
	}
}

// screenLogin is the device-code login the first-run screen runs: the same
// login as the terminal's, in the two steps the screen shows. Wait saves it.
type screenLogin struct {
	l      *sidecarLoginer
	ticket *loginTicket
}

func newScreenLogin(planeURL string) sidecartui.ManagedLogin {
	if planeURL == "" {
		return nil
	}
	// The approval page shows the name and lets the admin change it.
	return &screenLogin{l: &sidecarLoginer{planeURL: planeURL, name: defaultSidecarName(io.Discard), out: io.Discard,
		client: sidecarLoginHTTPClient(), minInterval: time.Second}}
}

func (s *screenLogin) Begin(ctx context.Context) (sidecartui.LoginTicket, error) {
	t, err := s.l.begin(ctx)
	if err != nil {
		return sidecartui.LoginTicket{}, err
	}
	s.ticket = t
	return sidecartui.LoginTicket{URL: t.start.VerificationURL, Code: t.start.UserCode}, nil
}

func (s *screenLogin) Wait(ctx context.Context) (sidecartui.ManagedPlane, error) {
	if s.ticket == nil {
		return sidecartui.ManagedPlane{}, errors.New("the login was not started")
	}
	login, err := s.l.wait(ctx, s.ticket)
	if err != nil {
		return sidecartui.ManagedPlane{}, err
	}
	if _, err := saveSidecarLogin(sidecarStateDir(), *login); err != nil {
		// The sidecar exists in the plane now; losing the token means
		// deleting it there, so say so rather than run once and forget.
		return sidecartui.ManagedPlane{}, fmt.Errorf("the sidecar %q was approved but its token could not be saved (%w); "+
			"delete it in the web app and log in again", login.Name, err)
	}
	return sidecartui.ManagedPlane{URL: login.URL, Token: login.Token, Name: login.Name, Page: login.Page}, nil
}

// screenLoginFor is the saved login a boot from the first-run screen runs
// on, or nil when it boots on no plane or on a plane connected by token.
func screenLoginFor(boot *sidecartui.Boot) *savedSidecarLogin {
	if boot.ControlPlaneURL == "" {
		return nil
	}
	login, err := loadSidecarLogin(sidecarStateDir(), boot.ControlPlaneURL)
	if err != nil || login == nil || login.Token != boot.Token {
		return nil
	}
	return login
}

// rememberSidecarConfig records the file a logged-in sidecar runs, so the
// next bare run boots it again. An empty path records nothing.
func rememberSidecarConfig(login *savedSidecarLogin, path string) error {
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	login.Config = abs
	_, err = saveSidecarLogin(sidecarStateDir(), *login)
	return err
}
