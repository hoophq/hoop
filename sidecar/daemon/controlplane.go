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
	lastRaw   []byte
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

func checkControlPlaneURL(value, source string) (string, string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", "", fmt.Errorf("%s holds %q, which is not an http(s) URL", source, value)
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
// The two do not merge. A plane-connected process serves what the plane
// sent, so a file that also declares listeners is refused rather than
// half-read: no operator can predict which half of a merged config wins, the
// same reason normalize refuses a field written in two spellings. The file
// keeps two jobs in plane mode: naming the URL, and naming a license
// (the documented precedence keeps the config file as a license source, and
// the plane sends none today).
func resolveConfigSource(local *Config, tokenFlag string) (*Config, error) {
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
	if local != nil && len(local.Listeners) > 0 {
		return nil, fmt.Errorf("the config file declares %d listener(s) and a control plane is configured (%s); "+
			"the control plane supplies the listeners, so remove them from the file or remove the control plane",
			len(local.Listeners), urlSource)
	}

	raw, err := fetchControlPlaneConfig(planeURL, token, Version)
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
	// which includes this process. The plane refuses to build a config for
	// a sidecar with no connections, so an empty one here is a bug worth
	// stopping on, not serving.
	if len(cfg.Listeners) == 0 {
		return nil, fmt.Errorf("the control plane at %s sent a config with no listeners", planeURL)
	}
	if local != nil && cfg.License == "" {
		cfg.License = local.License
	}
	cfg.ControlPlaneURL = planeURL
	cfg.cp = &controlPlane{url: planeURL, urlSource: urlSource, token: token, lastRaw: raw}
	return cfg, nil
}

// fetchControlPlaneConfig runs one handshake: it presents the token, reports
// the version, and returns the config document the plane answered with.
func fetchControlPlaneConfig(baseURL, token, version string) ([]byte, error) {
	body, err := json.Marshal(map[string]string{"version": version})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(baseURL, "/")+controlPlaneHandshakePath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("control plane request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(sidecarTokenHeader, token)

	resp, err := (&http.Client{Timeout: controlPlaneTimeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("the control plane at %s is unreachable: %w", baseURL, err)
	}
	defer resp.Body.Close()
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
	case http.StatusUnprocessableEntity:
		// An operator misconfiguration on the plane side (say, a connection
		// type no codec speaks). Retrying never fixes it; the message names
		// what to change.
		return nil, fmt.Errorf("the control plane at %s cannot build this sidecar's config: %s",
			baseURL, controlPlaneMessage(raw))
	default:
		return nil, fmt.Errorf("the control plane at %s answered %s: %s",
			baseURL, resp.Status, controlPlaneMessage(raw))
	}
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
// home is an outage. The heartbeat logs a changed config and applies
// nothing; listeners can appear and disappear between fetches, and swapping
// bound ports under live connections is a restart's job.
func (cp *controlPlane) heartbeat(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(heartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		changed, err := cp.poll()
		switch {
		case err != nil:
			log.Warn("control plane handshake failed; serving the last good config",
				"url", cp.url, "error", err)
		case changed:
			log.Warn("the control plane configuration changed; restart to apply it",
				"url", cp.url)
		}
	}
}

// poll fetches the current config and reports whether it differs from what
// this process is serving. lastRaw advances on change so one edit logs once,
// not once per tick. The byte compare is sound because the gateway builds
// the document with encoding/json, which orders struct fields by declaration
// and map keys alphabetically.
func (cp *controlPlane) poll() (changed bool, err error) {
	raw, err := fetchControlPlaneConfig(cp.url, cp.token, Version)
	if err != nil {
		return false, err
	}
	if bytes.Equal(raw, cp.lastRaw) {
		return false, nil
	}
	cp.lastRaw = raw
	return true, nil
}
