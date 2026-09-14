package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// The control plane connection is two facts, each with two sources.
//
//	URL:   HOOP_CONTROL_PLANE_URL, then the config file's "control_plane_url" key.
//	Token: the token flag, then HOOP_SIDECAR_TOKEN. Never a config key: a
//	       bearer secret does not belong in a file that gets committed.
//
// First wins, not first valid, the same rule license.Resolve follows: an env
// var holding garbage is an error rather than a reason to fall through,
// because a process that quietly ignored it will surprise the operator on
// the restart after the other source changes.
const (
	// ControlPlaneURLEnv outranks the config file's "control_plane_url" key.
	ControlPlaneURLEnv = "HOOP_CONTROL_PLANE_URL"
	// SidecarTokenEnv holds the token itself, not a path to one. The token
	// flag outranks it.
	SidecarTokenEnv = "HOOP_SIDECAR_TOKEN"

	// sidecarTokenHeader matches the gateway's SidecarAuthMiddleware.
	sidecarTokenHeader = "hoop-sidecar-token"
	// controlPlaneHandshakePath is appended to the base URL. The handshake
	// both authenticates and answers: the response body is the full config
	// this sidecar must serve, and the gateway records the reported version
	// as the sidecar's liveness.
	controlPlaneHandshakePath = "/api/sidecars/handshake"
	// controlPlaneConfigurationPath receives the import: a plane holding
	// no configuration is seeded with the local file's document through
	// it, once, on the first handshake that finds the plane empty.
	controlPlaneConfigurationPath = "/api/sidecars/configuration"

	controlPlaneTimeout = 15 * time.Second
	// maxControlPlaneConfig bounds the response read. A config is a few KB;
	// anything near this is a misdirected URL, not a big deployment.
	maxControlPlaneConfig = 8 << 20

	// heartbeatEvery is how often Run re-runs the handshake. It keeps the
	// gateway's last-seen fresh and notices a config edited in the UI; a
	// minute is fast enough for both and costs one small request.
	heartbeatEvery = time.Minute
)

// controlPlane is the resolved connection Setup reached: where to call, what
// to present, and the config bytes this process is serving, kept for the
// heartbeat's change detection.
type controlPlane struct {
	url       string
	urlSource string
	token     string
	// lastRaw is the startup handshake's document. The reloader seeds its
	// handled-tracking from it; the heartbeat itself keeps no compare
	// state.
	lastRaw []byte

	// imported reports that this boot seeded the plane with the local
	// file's document because the plane held none; Run logs it once.
	imported bool
	// fileListeners counts listeners the config file declared while the
	// plane already held the running config. They are ignored, never
	// merged; Run warns so an edit to the file that changed nothing is
	// not a silent mystery.
	fileListeners int

	// diskMode is the source this process is serving right now: the local
	// config file, rather than the document the plane holds. Startup sets
	// it from the handshake and handleAnswer moves it, because either flip
	// is applied to the running process, not deferred to a restart.
	diskMode bool
	// fileDoc is the config file's own document, retained so a plane that
	// releases this sidecar can be obeyed without a restart. Nil when the
	// process was started with no file, or with one declaring no listeners:
	// there is then nothing to switch to.
	fileDoc []byte
	// planeLicense is the license this process resolved from the plane, so a
	// license the plane moved is reported once rather than every minute. A
	// license cannot be swapped into a running process; only that fact waits
	// for a restart.
	planeLicense string
	// licenseOverridden reports that --license or HOOP_LICENSE is set, so
	// those outrank any license the plane sends. noteLicense stays silent
	// about a moved plane license then: a restart could not apply it.
	licenseOverridden bool
	// warned records the advice already given for the source this process
	// cannot act on, so it is said once per flip rather than every tick.
	warned bool

	// build is the PluginBuilder SetupWith received, retained so a reload
	// can rebuild the detector when the pii section drifts. Nil means the
	// entry point linked no detector; a drifted pii section then stays on
	// the restart path instead of swapping in rules the running detector
	// cannot serve.
	build PluginBuilder
}

// WithControlPlaneToken supplies the sidecar token from the command line,
// which outranks HOOP_SIDECAR_TOKEN. Only a caller with such a flag needs it;
// Setup reads the env var on its own. An empty value falls through, so an
// entry point hands its flag variable straight in without branching.
func WithControlPlaneToken(token string) Option {
	return func(o *setupOptions) { o.token = token }
}

// controlPlaneConfigured reports whether this process gets its listeners
// from a control plane. It reads the env var directly for the same reason
// ResolveLicense does: Validate runs inside LoadConfigBytes, before Setup
// resolves anything, and it would refuse a deployment configured through
// the environment alone for the listeners the plane holds.
func (c *Config) controlPlaneConfigured() bool {
	return c.ControlPlaneURL != "" || os.Getenv(ControlPlaneURLEnv) != ""
}

// ControlPlane reports the connection this config runs under, for logs and
// reports. The token stays out on purpose; only its existence is anyone's
// business.
func (c *Config) ControlPlane() (planeURL, source string, ok bool) {
	if c.cp == nil {
		return "", "", false
	}
	return c.cp.url, c.cp.urlSource, true
}

// resolveControlPlaneURL picks the URL, highest precedence first: the
// environment, then the config file's key. Empty everywhere means standalone.
func resolveControlPlaneURL(fileValue string) (value, source string, err error) {
	if v := os.Getenv(ControlPlaneURLEnv); v != "" {
		return checkControlPlaneURL(v, ControlPlaneURLEnv)
	}
	if fileValue != "" {
		return checkControlPlaneURL(fileValue, `the "control_plane_url" config key`)
	}
	return "", "", nil
}

// checkControlPlaneURL admits only what the handshake can use: an http(s)
// URL with a host, optionally a path prefix for a plane behind one. Query,
// fragment and userinfo are refused rather than carried into a request
// whose path would then not be the handshake's; the error names the source
// the operator has to fix.
func checkControlPlaneURL(value, source string) (string, string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", fmt.Errorf("%s holds %q, which is not an http(s) URL", source, value)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", "", fmt.Errorf("%s holds %q; a control plane URL carries no query, "+
			"fragment or user information, only a scheme, a host and an optional path prefix",
			source, value)
	}
	return value, source, nil
}

// resolveSidecarToken picks the token, highest precedence first: the command
// line, then HOOP_SIDECAR_TOKEN. Both hold the token itself, never a path.
func resolveSidecarToken(flagValue string) (value, source string) {
	if flagValue != "" {
		return flagValue, "the token flag"
	}
	if v := os.Getenv(SidecarTokenEnv); v != "" {
		return v, SidecarTokenEnv
	}
	return "", ""
}

// resolveConfigSource decides where the running config comes from: the local
// file alone (standalone, today's behavior), or the control plane handshake.
//
// The two never merge. A plane-connected process serves what the plane sent:
// no operator can predict which half of a merged config wins, the same
// reason normalize refuses a field written in two spellings. The file keeps
// three jobs in plane mode: naming the URL, naming a license (the documented
// precedence keeps the config file as a license source, and the plane sends
// none today), and seeding a plane that holds no configuration yet. That
// last one is the connect journey: a handshake answered "nothing is
// assigned" imports the file's whole document, so a standalone sidecar
// connects by adding the URL and passing the token, nothing else. Once the
// plane holds a configuration it owns it, and listeners still in the file
// are ignored out loud (Run warns), never merged.
func resolveConfigSource(local *Config, tokenFlag string) (*Config, error) {
	// A file saying what only the plane may say is refused rather than
	// ignored: two ways to spell the same instruction is two ways to get it
	// wrong, and "false" in a file the plane released is the contradiction
	// that would go unnoticed. Wrong with or without a plane, so it is
	// checked before the URL.
	if local != nil && local.LoadFromDisk != nil {
		return nil, errors.New(`"load_from_disk" is set by the control plane, not by this file; remove it`)
	}
	fileURL := ""
	if local != nil {
		fileURL = local.ControlPlaneURL
	}
	planeURL, urlSource, err := resolveControlPlaneURL(fileURL)
	if err != nil {
		return nil, err
	}
	token, tokenSource := resolveSidecarToken(tokenFlag)

	if planeURL == "" {
		if token != "" {
			// A token someone set that does nothing will surprise them the
			// day they rely on it. Same posture as a broken HOOP_LICENSE.
			return nil, fmt.Errorf("%s is set but no control plane is configured; "+
				"set %s or the \"control_plane_url\" config key, or drop the token", tokenSource, ControlPlaneURLEnv)
		}
		if local == nil {
			return nil, errors.New("no config file was given and no control plane is configured")
		}
		return local, nil
	}

	if token == "" {
		return nil, fmt.Errorf("a control plane is configured (%s) but no token was given; "+
			"pass the token flag or set %s", urlSource, SidecarTokenEnv)
	}

	raw, err := fetchControlPlaneConfig(planeURL, token, Version)
	if err == nil {
		if ans, ok := decodeAnswer(raw); ok && ans.releasedToDisk() {
			return useLocalConfig(local, ans, planeURL, urlSource, token, raw)
		}
	}
	imported := false
	// A plane with nothing to serve answers 412; an older gateway answers
	// 200 with a document naming no listeners. Same fact, same move: seed
	// the plane with the file's document, so the connect journey never
	// asks anyone to translate their YAML into an API call by hand.
	if errors.Is(err, errPlaneHasNoConfig) || (err == nil && !rawDeclaresListeners(raw)) {
		raw, imported, err = importLocalConfig(planeURL, token, local)
	}
	if err != nil {
		return nil, err
	}
	// The flip can land between the handshake and the import: the plane
	// refuses the push and serves the disk answer, which importLocalConfig
	// hands back untouched.
	if ans, ok := decodeAnswer(raw); ok && ans.releasedToDisk() {
		return useLocalConfig(local, ans, planeURL, urlSource, token, raw)
	}

	// The same strict decode, deprecation folding and validation the file
	// path gets. The gateway round-trips through this decoder before
	// answering, so a refusal here means the two builds disagree on the
	// schema, which the error should say out loud.
	cfg, err := LoadConfigBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("the control plane at %s sent a config this build cannot load "+
			"(the two versions may disagree on the schema): %w", planeURL, err)
	}
	// Validate relaxes the listener check whenever a plane is configured,
	// which includes this process. Listener-less answers were routed into
	// the import above, so reaching here without one means the config was
	// emptied between two requests: a bug worth stopping on, not serving.
	if len(cfg.Listeners) == 0 {
		return nil, fmt.Errorf("the control plane at %s sent a config with no listeners", planeURL)
	}
	// planeLicense is what the PLANE sent; cfg.License is reset to the file's
	// key so each keeps its own source in diagnostics. SetupWith ranks the
	// plane above the file and below --license and HOOP_LICENSE.
	planeLicense := cfg.License
	cfg.License = ""
	if local != nil {
		cfg.License = local.License
	}
	cfg.ControlPlaneURL = planeURL
	fileDoc, err := localDoc(local)
	if err != nil {
		return nil, err
	}
	cfg.cp = &controlPlane{
		url:          planeURL,
		urlSource:    urlSource,
		token:        token,
		lastRaw:      raw,
		imported:     imported,
		planeLicense: planeLicense,
		fileDoc:      fileDoc,
	}
	if !imported && local != nil {
		cfg.cp.fileListeners = len(local.Listeners)
	}
	return cfg, nil
}

// fetchControlPlaneConfig runs one handshake: it presents the token, reports
// the version, and returns the config document the plane answered with.
func fetchControlPlaneConfig(baseURL, token, version string) ([]byte, error) {
	body, err := json.Marshal(map[string]string{"version": version})
	if err != nil {
		return nil, err
	}
	// The base was validated by checkControlPlaneURL; JoinPath keeps a
	// path prefix (a plane behind /hoop) and normalizes trailing slashes.
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("control plane URL %q: %w", baseURL, err)
	}
	req, err := http.NewRequest(http.MethodPost,
		u.JoinPath(controlPlaneHandshakePath).String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("control plane request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(sidecarTokenHeader, token)

	resp, err := controlPlaneHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("the control plane at %s is unreachable: %w", baseURL, err)
	}
	// The body is always read to completion below; a close error after a
	// full read carries nothing actionable, so it is dropped on purpose.
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxControlPlaneConfig+1))
	if err != nil {
		return nil, fmt.Errorf("reading the control plane response from %s: %w", baseURL, err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		if len(raw) > maxControlPlaneConfig {
			return nil, fmt.Errorf("the control plane at %s answered with more than %d bytes; "+
				"check that the URL is the control plane and not something in front of it",
				baseURL, maxControlPlaneConfig)
		}
		return raw, nil
	case http.StatusUnauthorized:
		// Not self-healing: a mistyped token and a deleted sidecar both land
		// here, and the plane shows the token once at creation, so a lost
		// one means registering a new sidecar.
		return nil, fmt.Errorf("the control plane at %s rejected the token; "+
			"check it against the one shown when this sidecar was created", baseURL)
	case http.StatusPreconditionFailed:
		// The plane authenticated the token and holds nothing to serve.
		// resolveConfigSource turns this into an import when the local
		// file can supply the document; the heartbeat only logs it.
		return nil, fmt.Errorf("%w (at %s): %s", errPlaneHasNoConfig, baseURL, controlPlaneMessage(raw))
	case http.StatusUnprocessableEntity:
		// An operator misconfiguration on the plane side (say, a connection
		// type no codec speaks). Retrying never fixes it; the message names
		// what to change.
		return nil, fmt.Errorf("the control plane at %s cannot build this sidecar's config: %s",
			baseURL, controlPlaneMessage(raw))
	}

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, fmt.Errorf("the control plane at %s redirected to %q; the handshake "+
			"never follows one, so the token was not re-sent. Configure the final URL",
			baseURL, resp.Header.Get("Location"))
	}
	return nil, fmt.Errorf("the control plane at %s answered %s: %s",
		baseURL, resp.Status, controlPlaneMessage(raw))
}

// controlPlaneHTTPClient never follows a redirect: the token rides a custom
// header, which Go's redirect handling forwards even across origins (it
// strips only the headers it knows are credentials). Surfacing the 3xx turns
// a misdirected URL into an error naming the fix instead of a bearer token
// handed to whoever answered the Location.
func controlPlaneHTTPClient() *http.Client {
	return &http.Client{
		Timeout: controlPlaneTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// errPlaneHasNoConfig marks the handshake's 412: the plane authenticated the
// token and holds nothing to serve. resolveConfigSource turns it into an
// import when the local file can supply the document.
var errPlaneHasNoConfig = errors.New("the control plane has no configuration for this sidecar")

// errPlaneAlreadyConfigured marks the import's 409: a configuration landed
// on the plane between the handshake and the push. The concurrent author
// wins; the caller re-fetches and serves their document.
var errPlaneAlreadyConfigured = errors.New("the control plane already holds a configuration for this sidecar")

// rawDeclaresListeners reports whether the document names at least one
// listener, with a lenient probe rather than the strict decoder: it only
// picks between "serve this" and "seed the plane", and whatever is served
// still goes through LoadConfigBytes. Without it, an older gateway's empty
// 200 document would reach the strict path and blame a schema mismatch for
// what is really an unconfigured sidecar.
func rawDeclaresListeners(raw []byte) bool {
	var probe struct {
		Listeners []json.RawMessage `json:"listeners"`
	}
	return json.Unmarshal(raw, &probe) == nil && len(probe.Listeners) > 0
}

// decodeAnswer decodes the plane's answer leniently rather than through
// LoadConfigBytes: that path runs Validate, which refuses a document with no
// listeners, and a released sidecar's answer has none by design. False means
// the body is not a Config at all, which reaches the strict path, and that
// error names the real problem.
func decodeAnswer(raw []byte) (Config, bool) {
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, false
	}
	return cfg, true
}

// releasedToDisk reports the instruction this document carries: the sidecar's
// own config file is the source of truth, not this document.
func (c *Config) releasedToDisk() bool {
	return c.LoadFromDisk != nil && *c.LoadFromDisk
}

// useLocalConfig runs the local file while staying registered with the
// plane: the plane answered that this sidecar owns its own configuration, so
// only the license is taken from ans. Nothing else in that answer is obeyed,
// which is why it is neither normalized nor validated here.
func useLocalConfig(local *Config, ans Config, planeURL, urlSource, token string, raw []byte) (*Config, error) {
	if local == nil {
		return nil, fmt.Errorf("the control plane at %s says this sidecar loads its configuration "+
			"from disk, but no config file was given; restart with one, or turn load_from_disk "+
			"off in the control plane", planeURL)
	}
	if len(local.Listeners) == 0 {
		return nil, fmt.Errorf("the control plane at %s says this sidecar loads its configuration "+
			"from disk, but that file declares no listeners", planeURL)
	}
	// A copy, so this owns what it returns the way the plane path owns what
	// LoadConfigBytes gave it: a Loader is free to hand out a cached config,
	// and a second Setup must not inherit the first one's plane license,
	// resolved URL or connection.
	cfg := *local
	// The file's "license" key stays in cfg.License as the file-source
	// candidate. The plane's license rides cp.planeLicense, and SetupWith
	// ranks it above the file and below --license and HOOP_LICENSE, so a plane
	// license shows up as the plane's in diagnostics rather than the file's.
	cfg.ControlPlaneURL = planeURL
	fileDoc, err := localDoc(local)
	if err != nil {
		return nil, err
	}
	// lastRaw is the document this process SERVES, which here is the file's:
	// the reloader compares against it, so a later answer that moves the
	// source is drift it can act on and an unchanged one is silence.
	//
	// fileListeners stays zero: these listeners are being served, so Run's
	// "the config file's listeners are ignored" warning must not fire.
	cfg.cp = &controlPlane{
		url:          planeURL,
		urlSource:    urlSource,
		token:        token,
		lastRaw:      fileDoc,
		diskMode:     true,
		planeLicense: ans.License,
		fileDoc:      fileDoc,
	}
	return &cfg, nil
}

// localDoc renders the config file's own document, the one a process runs
// when the plane releases it. Nil, with no error, when there is no file to
// run: that is the state serveFileDoc advises about. The plane's URL and the
// license are connection facts this process already resolved, and the
// reloader compares documents, so they are left out the same way
// importLocalConfig leaves them out.
func localDoc(local *Config) ([]byte, error) {
	if local == nil || len(local.Listeners) == 0 {
		return nil, nil
	}
	doc := *local
	doc.ControlPlaneURL = ""
	doc.License = ""
	doc.cp = nil
	raw, err := json.Marshal(doc)
	if err != nil {
		// Collapsing this into nil would read as "no file to switch to" and
		// turn a release into wrong advice a boot later.
		return nil, fmt.Errorf("rendering the config file's document: %w", err)
	}
	return raw, nil
}

// importLocalConfig seeds a plane that holds no configuration with the local
// file's document, then returns what the plane serves afterwards, so the
// running config still round-trips through the plane even on the boot that
// taught it. pushed reports whether this process's write landed: a
// concurrent author wins the race and this boot serves their document.
//
// The pushed document drops control_plane_url and license. The URL is
// connection metadata this process already resolved, and the license stays
// a file-side source (the documented precedence), not a row every fleet
// admin can read.
func importLocalConfig(planeURL, token string, local *Config) (raw []byte, pushed bool, err error) {
	if local == nil || len(local.Listeners) == 0 {
		return nil, false, fmt.Errorf("the control plane at %s has no configuration for this sidecar; "+
			"author one in the control plane, or restart with a config file whose listeners this "+
			"process can import", planeURL)
	}
	doc := *local
	doc.ControlPlaneURL = ""
	doc.License = ""
	doc.cp = nil
	body, err := json.Marshal(doc)
	if err != nil {
		return nil, false, fmt.Errorf("marshaling the config file for the import: %w", err)
	}
	switch err := pushControlPlaneConfig(planeURL, token, body); {
	case err == nil:
		pushed = true
	case errors.Is(err, errPlaneAlreadyConfigured):
		// Someone authored a config between the two requests. Their write
		// wins; the fetch below serves it and Run warns about the file's
		// ignored listeners.
	default:
		return nil, false, err
	}
	raw, err = fetchControlPlaneConfig(planeURL, token, Version)
	if err != nil {
		return nil, false, fmt.Errorf("the handshake after the configuration import failed: %w", err)
	}
	// An admin who turned load_from_disk on between the handshake and the
	// push gets the refusal above and this answer: no listeners, by design.
	// The caller's disk-mode branch takes it from here.
	if ans, ok := decodeAnswer(raw); ok && ans.releasedToDisk() {
		return raw, false, nil
	}
	// A plane that accepted the import and still answers without listeners
	// is broken; naming that beats letting the strict decoder blame a
	// schema mismatch.
	if !rawDeclaresListeners(raw) {
		return nil, false, fmt.Errorf("the configuration was imported but the control plane at %s "+
			"still answers without listeners; check the plane's logs", planeURL)
	}
	return raw, pushed, nil
}

// pushControlPlaneConfig PUTs the document to the plane's configuration
// endpoint. The plane accepts it only when it holds no listeners yet;
// errPlaneAlreadyConfigured reports that refusal so the caller can serve
// the plane's document instead of overwriting it.
func pushControlPlaneConfig(baseURL, token string, body []byte) error {
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("control plane URL %q: %w", baseURL, err)
	}
	req, err := http.NewRequest(http.MethodPut,
		u.JoinPath(controlPlaneConfigurationPath).String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("control plane request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(sidecarTokenHeader, token)

	resp, err := controlPlaneHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("the control plane at %s is unreachable: %w", baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxControlPlaneConfig+1))
	if err != nil {
		return fmt.Errorf("reading the control plane response from %s: %w", baseURL, err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("the control plane at %s rejected the token; "+
			"check it against the one shown when this sidecar was created", baseURL)
	case http.StatusConflict:
		return fmt.Errorf("%w: %s", errPlaneAlreadyConfigured, controlPlaneMessage(raw))
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		// A gateway that predates the import route. The operator can still
		// author the configuration on the plane; the message says so
		// instead of blaming the config file.
		return fmt.Errorf("the control plane at %s does not accept a configuration import "+
			"(no PUT %s route; it may predate this build); author the configuration in the "+
			"control plane instead", baseURL, controlPlaneConfigurationPath)
	case http.StatusUnprocessableEntity:
		return fmt.Errorf("the control plane at %s refused the imported config: %s",
			baseURL, controlPlaneMessage(raw))
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return fmt.Errorf("the control plane at %s redirected to %q; the import "+
			"never follows one, so the token was not re-sent. Configure the final URL",
			baseURL, resp.Header.Get("Location"))
	}
	return fmt.Errorf("the control plane at %s answered %s: %s",
		baseURL, resp.Status, controlPlaneMessage(raw))
}

// controlPlaneMessage extracts the gateway's {"message": ...} error shape,
// falling back to the raw body so an unexpected proxy page is still visible.
func controlPlaneMessage(raw []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &m) == nil && m.Message != "" {
		return m.Message
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "no detail"
	}
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

// heartbeat re-runs the handshake until ctx ends. It keeps the gateway's
// last-seen fresh and notices a config edited in the UI.
//
// Failures degrade and never stop the process: the lanes keep serving the
// last good config, because killing a data-path proxy over a lost phone line
// home is an outage. handleAnswer decides what each fetched document means.
func (cp *controlPlane) heartbeat(ctx context.Context, log *slog.Logger, rl *reloader) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		raw, err := fetchControlPlaneConfig(cp.url, cp.token, Version)
		cp.handleAnswer(log, rl, raw, err)
	}
}

// handleAnswer routes one handshake result.
//
// Both the document and the SOURCE it comes from are drift, handled the same
// way: the reloader receives the document the answer selects — the plane's
// when it owns the configuration, the config file's when it has released it
// — and decides what a live process can do with it. Rule-only differences
// swap into the running lanes; anything a live process cannot change keeps
// the "restart to apply it" line, and a document whose failure can clear
// without another edit is retried (ADR-0014).
//
// So a flip in either direction is applied, not deferred. What cannot follow
// it live is the license, resolved once at startup, so a license the plane
// moved is reported instead.
func (cp *controlPlane) handleAnswer(log *slog.Logger, rl *reloader, raw []byte, err error) {
	if err != nil {
		// A plane that took the configuration back and holds nothing yet
		// answers 412. For a process running from its file that is the flip,
		// not a broken plane, and there is no document to apply: the next
		// start imports this file.
		if cp.diskMode && errors.Is(err, errPlaneHasNoConfig) {
			cp.advise(log, "the control plane took this sidecar's configuration back but holds none yet",
				"this process keeps serving its config file; the next start imports it into the control plane")
			return
		}
		log.Warn("control plane handshake failed; serving the last good config",
			"url", cp.url, "error", err)
		return
	}
	ans, decoded := decodeAnswer(raw)
	if decoded {
		// Whichever side owns the configuration, the license rides the same
		// answer and nonRuleDoc drops it, so the reloader will never report
		// it: this is the only place it is noticed.
		cp.noteLicense(log, ans.License)
	}
	if decoded && ans.releasedToDisk() {
		cp.serveFileDoc(log, rl)
		return
	}
	if cp.diskMode {
		log.Info("the control plane took this sidecar's configuration back; applying it")
		cp.diskMode = false
	}
	// Re-armed on every answer the plane owns, not only on a source change:
	// a release this process could not obey left advice standing that the
	// takeover made stale, and the next release deserves it again.
	cp.warned = false
	rl.handle(log, raw)
}

// serveFileDoc obeys an answer that released this sidecar's configuration:
// the file's own document goes to the reloader, which applies what it can
// and asks for a restart for the rest. An unchanged answer reaches the
// reloader's dedupe and does nothing.
func (cp *controlPlane) serveFileDoc(log *slog.Logger, rl *reloader) {
	if len(cp.fileDoc) == 0 {
		cp.advise(log, "the control plane says this sidecar loads its configuration from disk, "+
			"but this process was started without a config file declaring listeners",
			"restart with one, or turn load_from_disk off in the control plane")
		return
	}
	if !cp.diskMode {
		log.Info("the control plane released this sidecar's configuration; applying its config file")
		cp.diskMode, cp.warned = true, false
	}
	rl.handle(log, cp.fileDoc)
}

// noteLicense reports a license the plane moved. A license cannot be swapped
// into a running process: ResolveLicense ran at startup, above the file's
// key and below the flag and the env var. Reported once per change, because
// nothing about it moves again until the plane or the operator does.
//
// When --license or HOOP_LICENSE is set it stays silent: those outrank the
// plane, so a restart could not apply the moved license and "restart to apply
// it" would be wrong. The change is still tracked so it is not re-examined.
func (cp *controlPlane) noteLicense(log *slog.Logger, license string) {
	if license == cp.planeLicense {
		return
	}
	cp.planeLicense = license
	if cp.licenseOverridden {
		return
	}
	log.Warn("the license the control plane sends changed; restart to apply it")
}

// advise says, once per source flip, what an operator has to do about a
// state this process cannot act on. Repeating it every minute would say
// nothing new: nothing changes until the plane or the operator moves.
func (cp *controlPlane) advise(log *slog.Logger, msg, hint string) {
	if cp.warned {
		return
	}
	cp.warned = true
	log.Warn(msg, "hint", hint)
}
