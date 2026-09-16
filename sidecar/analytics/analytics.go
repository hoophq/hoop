// Package analytics reports product usage from a running sidecar to Segment.
//
// It is the sidecar's own client, written over the stdlib: the root module
// depends on libhoop and nothing else, and github.com/segmentio/analytics-go
// would be a second dependency for eighty lines of HTTP. The gateway has a
// Segment client too (gateway/analytics); it is not shared because it reads
// the gateway's config, models and org analytics mode, none of which exist
// in a sidecar process.
//
// # What leaves the process
//
// Counts and configuration shape, never content. No statement text, no
// identity subject, no rule name or pattern, no prompt, no token, no listener
// or upstream address. That is the same line the admin /config endpoint
// draws, and every property a caller adds must stay on this side of it.
//
// # Switching it off
//
// Two switches, either one enough. A binary built without a write key
// (`-ldflags -X github.com/hoophq/hoop/sidecar/analytics.writeKey=...`)
// sends nothing; so does HOOP_SIDECAR_ANALYTICS=off in the environment. Both
// produce a Client whose methods are no-ops, so callers never branch.
//
// # Adding an event
//
// Add the name to events.go, then call Track where the fact is known. The
// client fills the properties every event shares (version, entrypoint,
// platform, sidecar id); the caller supplies the ones specific to the event.
// Track never blocks the caller: events queue in memory and a full queue
// drops the newest, because the data path must not wait on telemetry.
package analytics

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// writeKey is the Segment source write key, stamped at build time. Empty
// disables the client, which is what a local build gets.
var writeKey string

// EnvVar switches analytics off at runtime. "off", "false" and "0" disable
// it; anything else, including unset, leaves the build-time decision alone.
const EnvVar = "HOOP_SIDECAR_ANALYTICS"

// endpoint is Segment's batch ingestion URL. A var rather than a const so
// a build can point it elsewhere with the same -X mechanism as writeKey,
// which is how the end-to-end smoke run captures what a real binary sends.
var endpoint = "https://api.segment.io/v1/batch"

// Library names this client in every message's context, so a Segment
// destination can tell sidecar traffic from the gateway's.
const Library = "hoop-sidecar"

// Batching parameters. Small on purpose: a sidecar emits a few events per
// hour, and the queue exists to absorb a burst at startup and shutdown, not
// to hold traffic.
const (
	queueSize     = 64
	batchSize     = 20
	flushEvery    = 10 * time.Second
	closeDeadline = 3 * time.Second
	sendTimeout   = 5 * time.Second
)

// Properties is one event's payload. Keys are kebab-case, matching the
// gateway's convention so one dashboard reads both.
type Properties map[string]any

// Options configures a Client. Every field is a fact about the process
// that the caller knows and the client does not.
type Options struct {
	// Version is the release the binary reports.
	Version string
	// Entrypoint is how the process was started: EntrypointCLI,
	// EntrypointBinary or EntrypointEmbedded.
	Entrypoint string
	// SidecarID identifies the install across events. See IDFromToken and
	// RandomID for the two ways to get one.
	SidecarID string
	// ControlPlane reports whether a control plane supplies the config.
	ControlPlane bool

	// Endpoint overrides the ingestion URL. Empty uses Segment's.
	Endpoint string
	// WriteKey overrides the build-time key. Empty uses it.
	WriteKey string
	// HTTPClient overrides the transport. Nil uses one with sendTimeout.
	HTTPClient *http.Client
}

// Entrypoint values, reported on every event.
const (
	EntrypointCLI      = "hoop"         // hoop start sidecar
	EntrypointBinary   = "hoop-inspect" // the standalone binary
	EntrypointEmbedded = "embedded"     // a caller of daemon.Run
)

// Client batches events and posts them to Segment. The zero value and a nil
// pointer are both valid, disabled clients: every method is a no-op on
// them, so a caller holds one field and never checks it.
type Client struct {
	opts   Options
	common Properties
	http   *http.Client

	queue chan message
	done  chan struct{}

	// mu guards dropped and closed. dropped counts events the full queue
	// refused, reported on the next event that gets through so the loss is
	// visible in the data. closed is set by Close before the queue is
	// closed, so a Track that arrives afterwards drops instead of sending
	// on a closed channel.
	mu      sync.Mutex
	dropped int
	closed  bool
}

// message is one Segment track call in the batch API's shape.
type message struct {
	Type        string     `json:"type"`
	MessageID   string     `json:"messageId"`
	AnonymousID string     `json:"anonymousId"`
	Event       string     `json:"event"`
	Properties  Properties `json:"properties"`
	Timestamp   string     `json:"timestamp"`
	Context     msgContext `json:"context"`
}

type msgContext struct {
	Library libraryContext `json:"library"`
}

type libraryContext struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Enabled reports whether a Client built from the environment and the
// build-time key would send anything. Exposed so a startup log can say
// which way the process went.
func Enabled() bool {
	return writeKey != "" && !disabledByEnv()
}

func disabledByEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvVar))) {
	case "off", "false", "0":
		return true
	}
	return false
}

// New builds a Client and starts its sender. It returns a disabled client
// when no write key is available or the environment turned analytics off;
// the caller uses it the same way either way, and Close is safe on both.
func New(opts Options) *Client {
	key := opts.WriteKey
	if key == "" {
		key = writeKey
	}
	if key == "" || disabledByEnv() {
		return &Client{}
	}
	opts.WriteKey = key
	if opts.Endpoint == "" {
		opts.Endpoint = endpoint
	}
	if opts.SidecarID == "" {
		opts.SidecarID = RandomID()
	}
	c := &Client{
		opts: opts,
		common: Properties{
			"version":                 opts.Version,
			"entrypoint":              opts.Entrypoint,
			"os":                      runtime.GOOS,
			"arch":                    runtime.GOARCH,
			"sidecar-id":              opts.SidecarID,
			"control-plane-connected": opts.ControlPlane,
		},
		http:  opts.HTTPClient,
		queue: make(chan message, queueSize),
		done:  make(chan struct{}),
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: sendTimeout}
	}
	go c.run()
	return c
}

// Enabled reports whether this client sends anything.
func (c *Client) Enabled() bool { return c != nil && c.queue != nil }

// Track queues one event. The common properties are added to props; a key
// the caller set wins, so an event can override "version" if it must. Never
// blocks and never panics: a full queue or a closed client drops the event
// and counts the drop.
func (c *Client) Track(event Event, props Properties) {
	if !c.Enabled() {
		return
	}
	out := make(Properties, len(c.common)+len(props)+1)
	for k, v := range c.common {
		out[k] = v
	}
	for k, v := range props {
		out[k] = v
	}
	m := message{
		Type:        "track",
		MessageID:   RandomID(),
		AnonymousID: c.opts.SidecarID,
		Event:       string(event),
		Properties:  out,
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		Context:     msgContext{Library: libraryContext{Name: Library, Version: c.opts.Version}},
	}

	// The send happens under mu so Close cannot close the queue between
	// the closed check and the send. Non-blocking, so the lock is held
	// for a channel operation and nothing else.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		c.dropped++
		return
	}
	if c.dropped > 0 {
		out["dropped-events"] = c.dropped
		c.dropped = 0
	}
	select {
	case c.queue <- m:
	default:
		c.dropped++
	}
}

// Close flushes what is queued and stops the sender. It waits at most
// closeDeadline, because a shutdown must not hang on Segment. Idempotent,
// and a Track after it is dropped rather than a crash.
func (c *Client) Close() {
	if !c.Enabled() {
		return
	}
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		close(c.queue)
	}
	c.mu.Unlock()
	select {
	case <-c.done:
	case <-time.After(closeDeadline):
	}
}

// run drains the queue into batches: a batch goes out when it fills, when
// the flush timer fires, or when Close ends the queue.
//
// A panic anywhere in the sender is swallowed and ends the goroutine. This
// is the one goroutine in the process whose death must not matter: the
// relay keeps serving, Track keeps dropping into a queue nobody drains, and
// Close returns at its deadline. Letting it propagate would let a telemetry
// bug take down a data-path proxy.
func (c *Client) run() {
	defer close(c.done)
	defer func() { _ = recover() }()
	batch := make([]message, 0, batchSize)
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		c.send(batch)
		batch = batch[:0]
	}
	for {
		select {
		case m, ok := <-c.queue:
			if !ok {
				flush()
				return
			}
			batch = append(batch, m)
			if len(batch) >= batchSize {
				flush()
			}
		case <-t.C:
			flush()
		}
	}
}

// send posts one batch. Failures are dropped: there is no retry, no log
// and no error to return, because nothing in the process can act on a
// telemetry failure and the data path must never learn about one.
func (c *Client) send(batch []message) {
	body, err := json.Marshal(map[string]any{
		"batch":  batch,
		"sentAt": time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.opts.Endpoint, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// Segment's HTTP API authenticates with the write key as the basic-auth
	// user and an empty password.
	req.SetBasicAuth(c.opts.WriteKey, "")
	resp, err := c.http.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// IDFromToken derives a stable sidecar id from the control plane token: the
// same install reports under the same id across restarts, and the token
// itself never leaves the process. SHA-256, hex.
func IDFromToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// RandomID returns a fresh 128-bit hex id. A standalone sidecar has no
// durable identity — no token, no state directory — so it gets one per
// process, and a dashboard reads restarts as new installs. That is the
// honest reading: nothing ties the two processes together.
func RandomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is a broken host; the id only needs to be
		// unique enough for a telemetry profile, so fall back to the clock.
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}
