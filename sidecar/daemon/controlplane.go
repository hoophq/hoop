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

	// LicenseManagedHeader is how a plane says it owns the licensing
	// decision. A gateway that predates the feature sends nothing, and the
	// difference is not visible in the document: an organization with no
	// license and a gateway that knows nothing about licenses both answer
	// without a `license` key.
	//
	// It decides whether an absent license means "the organization has
	// none" (ignore the local sources) or "this gateway cannot tell you"
	// (keep them). Reading it wrong costs an operator their caps, and a
	// config that needs them does not start at all.
	//
	// Exported because the gateway sets what this reads. One constant, in
	// the module that has to parse it: two string literals agreeing today
	// is not a contract, and a rename on one side would disable the signal
	// in silence rather than fail a build.
	LicenseManagedHeader = "hoop-sidecar-license-managed"

	// ConfigRevisionHeader carries the plane's name for the document it just
	// answered with. The sidecar sends it back on the next handshake beside
	// what it did with it, which is the only way the plane can tell a
	// sidecar running its rules from one that refused them and kept the old
	// ones.
	//
	// A HEADER for the same reason LicenseManagedHeader is one: the document
	// is decoded with DisallowUnknownFields, so a new key in it would be a
	// startup failure on every build that predates this.
	//
	// The value is opaque. The plane issues it and compares it to itself;
	// the sidecar never parses it.
	ConfigRevisionHeader = "hoop-sidecar-config-revision"

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

	// license is the document the plane sent, empty when its organization
	// holds none. It is the ONLY license a plane-connected process runs
	// under -- when licenseManaged says so; Config.License is emptied in
	// plane mode so no reader mistakes the file's key for something in
	// force.
	license string
	// licenseManaged reports that the plane answered with
	// licenseManagedHeader, so an absent license is an answer rather than
	// a silence. False for a gateway older than this feature, and then the
	// local sources rank as they always did.
	licenseManaged bool
	// ignoredLicense names the local source this process is NOT using, so
	// Run can say so once. Empty when no local source held a document.
	ignoredLicense string

	// every overrides the heartbeat interval. Zero means heartbeatEvery,
	// which is what every deployment runs; a test sets it so a case about
	// what a heartbeat does is not also a case about waiting a minute.
	every time.Duration
	// revision is the ConfigRevisionHeader of the document this process is
	// RUNNING, and outcome what handling it concluded. They are reported on
	// the next handshake so the plane can tell a sidecar that took its
	// document from one that refused it and kept the old rules. Empty until
	// the plane says a revision at all -- an older gateway sends none, and
	// the plane then reports the sidecar as unknown rather than guessing.
	revision string
	outcome  string

	// imported reports that this boot seeded the plane with the local
	// file's document because the plane held none; Run logs it once.
	imported bool
	// fileListeners counts listeners the config file declared while the
	// plane already held the running config. They are ignored, never
	// merged; Run warns so an edit to the file that changed nothing is
	// not a silent mystery.
	fileListeners int
	// diskMode reports the plane answered load_from_disk: the file's
	// document is serving, the heartbeat still runs, and the reloader
	// hot-applies license drift and ownership flips when the documents
	// allow it, restarting only for drift no reload can absorb.
	diskMode bool
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
// two jobs in plane mode: naming the URL, and seeding a plane that holds no
// configuration yet. Its `license` key is not one of them -- the plane owns
// the license as well, and a local one is ignored out loud. That
// last one is the connect journey: a handshake answered "nothing is
// assigned" imports the file's whole document, so a standalone sidecar
// connects by adding the URL and passing the token, nothing else. Once the
// plane holds a configuration it owns it, and listeners still in the file
// are ignored out loud (Run warns), never merged.
//
// A plane answering "load_from_disk" hands the document back to the file:
// the file's listeners serve, while the connection and the license stay
// plane-side facts (see licenseManaged).
func resolveConfigSource(local *Config, tokenFlag string) (*Config, error) {
	if local != nil && local.LoadFromDisk != nil {
		return nil, errors.New(`"load_from_disk" is not a config file key; ` +
			`it is set on the control plane's sidecar configuration, and the file cannot answer for the plane`)
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

	// The first handshake reports nothing about a previous document: this
	// process has handled none, and claiming otherwise would let a plane
	// read a fresh boot as a sidecar that had already applied something.
	answer, err := fetchControlPlaneConfig(planeURL, token, handshakeRequest{Version: Version})
	raw, managed := answer.raw, answer.managed
	var planeDoc Config
	if err == nil && json.Unmarshal(raw, &planeDoc) == nil &&
		planeDoc.LoadFromDisk != nil && *planeDoc.LoadFromDisk {
		if local == nil {
			return nil, fmt.Errorf("the control plane at %s says this sidecar loads its "+
				"configuration from disk, but no config file was given", planeURL)
		}
		if len(local.Listeners) == 0 {
			return nil, fmt.Errorf("the control plane at %s says this sidecar loads its "+
				"configuration from disk, but the config file declares no listeners", planeURL)
		}
		// The file's `license` key stays on the document -- local IS the
		// running config here -- but under a managing plane it is not a
		// source (resolveLicenseFor), and SetupWith names it as ignored.
		local.ControlPlaneURL = planeURL
		local.cp = &controlPlane{url: planeURL, urlSource: urlSource, token: token,
			lastRaw: raw, diskMode: true, license: planeDoc.License, licenseManaged: managed,
			revision: answer.revision}
		return local, nil
	}
	imported := false
	// A plane with nothing to serve answers 412; an older gateway answers
	// 200 with a document naming no listeners. Same fact, same move: seed
	// the plane with the file's document, so the connect journey never
	// asks anyone to translate their YAML into an API call by hand.
	if errors.Is(err, errPlaneHasNoConfig) || (err == nil && !rawDeclaresListeners(raw)) {
		answer, imported, err = importLocalConfig(planeURL, token, local)
		raw, managed = answer.raw, answer.managed
	}
	if err != nil {
		return nil, err
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
	// The plane's license moves to the connection. The file's key comes
	// back only when the plane does NOT manage licensing: under a managing
	// plane it is not a source, and leaving it on the Config would report a
	// license this process does not run under; against an older gateway it
	// is still the operator's license and has to keep working.
	planeLicense := cfg.License
	cfg.License = ""
	if !managed && local != nil {
		cfg.License = local.License
	}
	cfg.ControlPlaneURL = planeURL
	// The boot document counts as applied: this process is about to run on
	// it, and anything that would refuse it (the strict decode above, the
	// caps in buildLanes) stops the process before it ever handshakes. So a
	// sidecar that reaches its first heartbeat has, by construction, applied
	// what it reports here.
	cfg.cp = &controlPlane{url: planeURL, urlSource: urlSource, token: token,
		lastRaw: raw, imported: imported, license: planeLicense, licenseManaged: managed,
		revision: answer.revision, outcome: reloadApplied.String()}
	if !imported && local != nil {
		cfg.cp.fileListeners = len(local.Listeners)
	}
	return cfg, nil
}

// handshakeRequest is what a sidecar tells the plane about itself. Every
// field past version is optional and omitted until the sidecar has actually
// handled a document, so a plane reading this cannot mistake "nothing has
// happened yet" for "the last document was applied".
type handshakeRequest struct {
	Version string `json:"version"`
	// AppliedRevision is the ConfigRevisionHeader of the last document this
	// sidecar HANDLED -- which is not the last one it was served. A document
	// that was refused or needs a restart leaves this at the revision still
	// running, which is the honest answer.
	AppliedRevision string `json:"applied_revision,omitempty"`
	// LastOutcome is reloadOutcome.String() for that document.
	LastOutcome string `json:"last_outcome,omitempty"`
}

// handshakeAnswer is one handshake's result: the document, and the two facts
// the plane reports beside it rather than inside it.
type handshakeAnswer struct {
	raw      []byte
	managed  bool
	revision string
}

// fetchControlPlaneConfig runs one handshake: it presents the token, reports
// what this sidecar is running, and returns the config document the plane
// answered with.
//
// managed reports whether the plane claimed the licensing decision, and
// revision is the plane's name for this document. Both ride beside the
// document rather than in it, so an older gateway simply does not say them;
// see licenseManagedHeader.
func fetchControlPlaneConfig(baseURL, token string, hs handshakeRequest) (answer handshakeAnswer, err error) {
	body, merr := json.Marshal(hs)
	if merr != nil {
		return handshakeAnswer{}, merr
	}
	// The base was validated by checkControlPlaneURL; JoinPath keeps a
	// path prefix (a plane behind /hoop) and normalizes trailing slashes.
	u, perr := url.Parse(baseURL)
	if perr != nil {
		return handshakeAnswer{}, fmt.Errorf("control plane URL %q: %w", baseURL, perr)
	}
	req, rerr := http.NewRequest(http.MethodPost,
		u.JoinPath(controlPlaneHandshakePath).String(), bytes.NewReader(body))
	if rerr != nil {
		return handshakeAnswer{}, fmt.Errorf("control plane request: %w", rerr)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(sidecarTokenHeader, token)

	resp, derr := controlPlaneHTTPClient().Do(req)
	if derr != nil {
		return handshakeAnswer{}, fmt.Errorf("the control plane at %s is unreachable: %w", baseURL, derr)
	}
	// The body is always read to completion below; a close error after a
	// full read carries nothing actionable, so it is dropped on purpose.
	defer func() { _ = resp.Body.Close() }()
	raw, aerr := io.ReadAll(io.LimitReader(resp.Body, maxControlPlaneConfig+1))
	if aerr != nil {
		return handshakeAnswer{}, fmt.Errorf("reading the control plane response from %s: %w", baseURL, aerr)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		if len(raw) > maxControlPlaneConfig {
			return handshakeAnswer{}, fmt.Errorf("the control plane at %s answered with more than %d bytes; "+
				"check that the URL is the control plane and not something in front of it",
				baseURL, maxControlPlaneConfig)
		}
		return handshakeAnswer{
			raw:      raw,
			managed:  resp.Header.Get(LicenseManagedHeader) == "true",
			revision: resp.Header.Get(ConfigRevisionHeader),
		}, nil
	case http.StatusUnauthorized:
		// Not self-healing: a mistyped token and a deleted sidecar both land
		// here, and the plane shows the token once at creation, so a lost
		// one means registering a new sidecar.
		return handshakeAnswer{}, fmt.Errorf("the control plane at %s rejected the token; "+
			"check it against the one shown when this sidecar was created", baseURL)
	case http.StatusPreconditionFailed:
		// The plane authenticated the token and holds nothing to serve.
		// resolveConfigSource turns this into an import when the local
		// file can supply the document; the heartbeat only logs it.
		return handshakeAnswer{}, fmt.Errorf("%w (at %s): %s", errPlaneHasNoConfig, baseURL, controlPlaneMessage(raw))
	case http.StatusUnprocessableEntity:
		// An operator misconfiguration on the plane side (say, a connection
		// type no codec speaks). Retrying never fixes it; the message names
		// what to change.
		return handshakeAnswer{}, fmt.Errorf("the control plane at %s cannot build this sidecar's config: %s",
			baseURL, controlPlaneMessage(raw))
	}

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return handshakeAnswer{}, fmt.Errorf("the control plane at %s redirected to %q; the handshake "+
			"never follows one, so the token was not re-sent. Configure the final URL",
			baseURL, resp.Header.Get("Location"))
	}
	return handshakeAnswer{}, fmt.Errorf("the control plane at %s answered %s: %s",
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

// reimport answers a 412 on the heartbeat. The plane empties its document
// when an admin hands a disk-mode sidecar back to it, and this process then
// pushes its config file, the same import startup runs. A plane-owned process
// has no file to push that the plane did not already refuse, so it only logs.
func (cp *controlPlane) reimport(log *slog.Logger, rl *reloader) (handshakeAnswer, error) {
	if !rl.diskMode || rl.configPath == "" || rl.load == nil {
		return handshakeAnswer{}, fmt.Errorf("%w (at %s)", errPlaneHasNoConfig, cp.url)
	}
	local, err := rl.load(rl.configPath)
	if err != nil {
		return handshakeAnswer{}, fmt.Errorf("the control plane asks for the config file, "+
			"but it does not load: %w", err)
	}
	answer, _, err := importLocalConfig(cp.url, cp.token, local)
	if err != nil {
		return handshakeAnswer{}, err
	}
	log.Info("imported the config file into the control plane, which now owns it", "path", rl.configPath)
	return answer, nil
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

// importLocalConfig seeds a plane that holds no configuration with the local
// file's document, then returns what the plane serves afterwards, so the
// running config still round-trips through the plane even on the boot that
// taught it. pushed reports whether this process's write landed: a
// concurrent author wins the race and this boot serves their document.
//
// The pushed document drops control_plane_url and license. The URL is
// connection metadata this process already resolved, and the license belongs
// to the organization, not to this sidecar's row: the plane serves its own
// on every handshake, and a copy stored here would go stale the day it is
// renewed. The plane refuses the key as well; this keeps the refusal from
// ever being reached.
func importLocalConfig(planeURL, token string, local *Config) (answer handshakeAnswer, pushed bool, err error) {
	if local == nil || len(local.Listeners) == 0 {
		return handshakeAnswer{}, false, fmt.Errorf("the control plane at %s has no configuration for this sidecar; "+
			"author one in the control plane, or restart with a config file whose listeners this "+
			"process can import", planeURL)
	}
	doc := *local
	doc.ControlPlaneURL = ""
	doc.License = ""
	doc.cp = nil
	body, err := json.Marshal(doc)
	if err != nil {
		return handshakeAnswer{}, false, fmt.Errorf("marshaling the config file for the import: %w", err)
	}
	switch err := pushControlPlaneConfig(planeURL, token, body); {
	case err == nil:
		pushed = true
	case errors.Is(err, errPlaneAlreadyConfigured):
		// Someone authored a config between the two requests. Their write
		// wins; the fetch below serves it and Run warns about the file's
		// ignored listeners.
	default:
		return handshakeAnswer{}, false, err
	}
	answer, err = fetchControlPlaneConfig(planeURL, token, handshakeRequest{Version: Version})
	if err != nil {
		return handshakeAnswer{}, false, fmt.Errorf("the handshake after the configuration import failed: %w", err)
	}
	// A plane that accepted the import and still answers without listeners
	// is broken; naming that beats letting the strict decoder blame a
	// schema mismatch.
	if !rawDeclaresListeners(answer.raw) {
		return handshakeAnswer{}, false, fmt.Errorf("the configuration was imported but the control plane at %s "+
			"still answers without listeners; check the plane's logs", planeURL)
	}
	return answer, pushed, nil
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
// home is an outage. Every fetched document goes to the reloader, which
// owns the seen/handled bookkeeping: it swaps rule-only drift into the
// running lanes, answers "restart to apply it" for everything a live
// process cannot change, and retries a document whose failure can clear
// without another edit (ADR-0014).
func (cp *controlPlane) heartbeat(ctx context.Context, log *slog.Logger, rl *reloader) {
	every := cp.every
	if every == 0 {
		every = heartbeatEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		// What this sidecar did with the LAST document rides on this
		// request. Reporting it is the whole reason the plane can render a
		// fleet state at all: a document that was refused, or that needs a
		// restart, leaves the process serving its old rules while still
		// handshaking on time, and nothing else distinguishes that from a
		// sidecar that took the document.
		answer, err := fetchControlPlaneConfig(cp.url, cp.token, handshakeRequest{
			Version:         Version,
			AppliedRevision: cp.revision,
			LastOutcome:     cp.outcome,
		})
		if errors.Is(err, errPlaneHasNoConfig) {
			answer, err = cp.reimport(log, rl)
		}
		if err != nil {
			rl.tel.heartbeatFailed()
			log.Warn("control plane handshake failed; serving the last good config",
				"url", cp.url, "error", err)
			continue
		}
		outcome := rl.handle(log, answer.raw)
		cp.outcome = outcome.String()
		// The revision moves only when the document was actually taken on.
		// reloadUnchanged is the steady state and re-reports what is
		// running; every other outcome means the running rules are still
		// the previous revision's, so claiming the new one would report
		// convergence the process has not reached.
		if outcome == reloadApplied || outcome == reloadUnchanged {
			cp.revision = answer.revision
		}
	}
}
