package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Paths the gateway serves. Relative to the configured api_url, which is the
// gateway root rather than its API prefix, because that is the URL an operator
// already has written down for everything else hoop.
const (
	claimPath  = "/api/relay/reviews/claim"
	createPath = "/api/relay/reviews"
)

// DefaultTimeout bounds one gateway call.
//
// Short, because the call sits inline on a proxied connection. The same
// warning the analyzer carries applies at a smaller magnitude: a slow inline
// call can outlive an upstream's keep-alive.
const DefaultTimeout = 5 * time.Second

// ErrNoApproval reports that no approved, unconsumed review exists for this
// statement. It is the ordinary answer on a first attempt, not a failure.
var ErrNoApproval = errors.New("hoopinspect/review: no approved review for this statement")

// errNotFound marks any 404 from the gateway, so one endpoint can read it as
// "no approval" while the others keep it as the failure it is.
var errNotFound = errors.New("hoopinspect/review: gateway returned 404")

// Ticket is one review as the gateway reports it.
type Ticket struct {
	ReviewID  string `json:"review_id"`
	SessionID string `json:"session_id"`

	// Status is the review's status at the moment of the call: PENDING on a
	// freshly filed review, EXECUTED on one this call just consumed.
	Status string `json:"status"`

	// URL points a human at the review in the webapp. It is put in front of
	// the end user in the protocol's error frame, so it must stay free of
	// anything the statement carried.
	URL string `json:"url"`
}

// Request files a review for one statement.
//
// Statement carries the canonical text a human will read and approve. It is
// the one field here that holds content from the wire, and it goes no further
// than the review the operator's own reviewers will open.
type Request struct {
	Connection    string `json:"connection"`
	StatementHash string `json:"statement_hash"`
	Statement     string `json:"statement"`

	// Marker is the request identity: an existing PENDING review filed under
	// the same marker is returned rather than duplicated. Empty always
	// creates, which is the safe default and the reason require_marker
	// exists for a busy lane.
	Marker string `json:"marker,omitempty"`

	// RiskLevel and Rule say why the statement was gated, so the reviewer
	// sees the analyzer's verdict beside the text. Classifications only:
	// never the model's prose, which can quote the statement back.
	RiskLevel string `json:"risk_level,omitempty"`
	Rule      string `json:"rule,omitempty"`
}

// Client calls the hoop gateway's review endpoints.
//
// Concrete on purpose. There is one deployment shape (the sidecar beside
// Envoy) and one transport (the gateway's HTTPS API), so an interface with a
// single implementation would be indirection to read through for nothing. The
// one thing it would have bought is a test seam, and httptest already provides
// a better one: tests assert against real request bodies, which also covers
// the encoding.
//
// See analyzer/anthropic for the same shape against a different API. Like it,
// this costs no dependency: a bearer token against a JSON endpoint needs
// net/http and encoding/json and nothing else, so the module stays
// dependency-free.
//
// Safe for concurrent use.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient builds a client for the gateway at baseURL.
//
// token is the sandbox's hpk_ credential. It identifies the environment, and
// every endpoint derives org and owner from it: nothing the caller puts in a
// request body can widen what it reaches.
func NewClient(baseURL, token string, timeout time.Duration) (*Client, error) {
	if err := ValidateAPIURL(baseURL); err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("hoopinspect/review: empty token")
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: timeout},
	}, nil
}

// ValidateAPIURL refuses an api_url that could not work or that would leak the
// credential it is called with.
//
// Exported so the config layer refuses a bad URL at startup rather than on the
// first gated statement, and so both checks are the same check.
func ValidateAPIURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("hoopinspect/review: empty api_url")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("hoopinspect/review: api_url is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("hoopinspect/review: api_url scheme %q is not http or https", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("hoopinspect/review: api_url has no host")
	}
	if u.User != nil {
		return errors.New("hoopinspect/review: api_url carries credentials in its userinfo; use token_file")
	}
	if u.RawQuery != "" {
		return errors.New("hoopinspect/review: api_url carries a query string; a credential there would be published by /config")
	}
	return nil
}

// String renders a placeholder rather than the struct.
//
// The token is a plain string here rather than an analyzer.Secret, because the
// one place it is used is the line that builds the Authorization header and
// wrapping it would buy nothing there. What the wrapper WOULD have bought is
// this: a %v on a Client, or on any future struct holding one, printing the
// credential. So the Client refuses to render, and a field added beside the
// token cannot leak by omission of a tag.
func (c *Client) String() string { return "review.Client{" + c.Endpoint() + "}" }

// GoString covers %#v, which ignores Stringer.
func (c *Client) GoString() string { return c.String() }

// Endpoint reports the gateway host this client talks to, for the startup log
// and the /config view. Host only: never a path, and never anything that could
// carry a token.
func (c *Client) Endpoint() string {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return ""
	}
	return u.Host
}

type claimRequest struct {
	Connection    string `json:"connection"`
	StatementHash string `json:"statement_hash"`
}

// Claim atomically consumes an approved review for this exact statement, or
// reports ErrNoApproval.
//
// It is the AUTHORIZATION step, not a read. The gateway moves the review to
// EXECUTED in the same statement that selects it, so two connections racing on
// one approval means exactly one of them gets a row back and the other is
// denied. A lookup followed by a settle would leave a window between deciding
// and consuming.
//
// Claiming before forwarding means an upstream failure consumes an approval
// for an execution that never happened, and a human has to approve again. The
// alternative — forward, then settle — risks executing without consuming,
// which is strictly worse. Fail toward asking the human again.
func (c *Client) Claim(ctx context.Context, connection, statementHash string) (*Ticket, error) {
	t, err := c.post(ctx, claimPath, claimRequest{
		Connection:    connection,
		StatementHash: statementHash,
	})
	// Only HERE does a 404 mean "no approval". It is the endpoint's ordinary
	// answer on a first attempt, so it is translated into the sentinel the
	// gate branches on rather than reported as a failure.
	if errors.Is(err, errNotFound) {
		return nil, ErrNoApproval
	}
	return t, err
}

// Request files a review, or returns the PENDING one already filed under the
// same marker.
//
// A 404 here is NOT ErrNoApproval. The create path answers 404 when the
// connection cannot be resolved under the caller's own access — a
// misconfigured lane, a name that does not exist, a token whose groups do not
// reach it — and reporting that as "no approved review for this statement"
// sends an operator looking for a review queue when what is wrong is the
// connection name in their config.
func (c *Client) Request(ctx context.Context, r Request) (*Ticket, error) {
	return c.post(ctx, createPath, r)
}

func (c *Client) post(ctx context.Context, path string, payload any) (*Ticket, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("hoopinspect/review: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("hoopinspect/review: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hoopinspect/review: %s: %w", path, err)
	}
	defer resp.Body.Close()

	// Bounded: an error body is rendered into a denial message the end user
	// reads, and a gateway that answers with a megabyte of HTML must not put
	// a megabyte into a Postgres ErrorResponse.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("hoopinspect/review: %s: read response: %w", path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		err := fmt.Errorf("hoopinspect/review: %s: gateway returned %d: %s",
			path, resp.StatusCode, apiMessage(raw))
		if resp.StatusCode == http.StatusNotFound {
			// Wrapped rather than replaced: Claim needs to recognize a 404,
			// and every other caller needs to keep the gateway's own reason.
			return nil, fmt.Errorf("%w: %w", errNotFound, err)
		}
		return nil, err
	}

	var t Ticket
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("hoopinspect/review: %s: decode response: %w", path, err)
	}
	if t.ReviewID == "" {
		return nil, fmt.Errorf("hoopinspect/review: %s: gateway returned no review id", path)
	}
	return &t, nil
}

// apiMessage pulls the gateway's own error text out of a failure body, so an
// operator reading a denial sees "connection not found" rather than a JSON
// blob. Falls back to a bounded prefix of whatever arrived.
func apiMessage(raw []byte) string {
	var envelope struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Message != "" {
		return envelope.Message
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 256 {
		return s[:256] + "…"
	}
	return s
}
