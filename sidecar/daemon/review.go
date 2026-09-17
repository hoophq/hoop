package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// controlPlaneReviewsPath files a statement for human approval.
//
// The plane answers the same question on every call: is there a review for
// these exact bytes, and may this statement move. It files one when there is
// none, returns the live one when there is, and consumes an approved one
// once. The sidecar therefore has no poll and keeps no review state: it asks
// again on the retry and reads the answer.
const controlPlaneReviewsPath = "/api/sidecars/reviews"

// maxReviewResponse bounds the response read. A review answer is a few
// hundred bytes; anything near this is not the control plane.
const maxReviewResponse = 1 << 20

// reviewFunc is what analyzer.Config.Review takes. An alias rather than a
// defined type, so the value assigns straight onto the field.
type reviewFunc = func(context.Context, string) (analyzer.ReviewResult, error)

// reviewer returns the call one lane makes to hold a statement, with the two
// facts the plane authorizes against bound in: the listener the statement
// arrived on and the approval rule that lane named.
//
// Both are per lane, and neither is on analyzer.Config: the evaluator never
// reads them, it would only copy them into a request body, and the listener
// is NOT the evaluator's own name: a deprecated ai_analysis rule carries an
// operator-chosen one. Binding them here keeps the analyzer package unable to
// get that wrong.
//
// A nil control plane returns a nil func, which denies at evaluation time.
// That is the -validate path: it builds every lane without ever contacting a
// plane, so a client cannot exist there, and a build error would refuse a
// config that runs correctly.
func (cp *controlPlane) reviewer(listener, rule string) reviewFunc {
	if cp == nil {
		return nil
	}
	return func(ctx context.Context, statement string) (analyzer.ReviewResult, error) {
		return cp.fileReview(ctx, listener, rule, statement)
	}
}

// holdsWithoutAPlane reports a lane that holds statements for approval in a
// process that can file none.
//
// It is checked in buildLanes rather than in Config.Validate, and the reason
// is the plane-served document: Validate runs inside LoadConfigBytes on
// whatever bytes arrived, and a document the plane sent carries no
// control_plane_url, and resolveConfigSource puts the resolved URL back on the
// Config afterwards. Refusing on the document would therefore refuse the
// normal deployment, where the URL lives in the local file and the listeners
// come from the plane. The same trap the license caps avoid by living here.
//
// Two ways to have one, because two callers mean different things. A running
// process HAS a connection (ac.cp), including every heartbeat reload, where
// the reloaded document is again missing the key. A -validate run has no
// connection by construction and is asking about the file, so a URL named in
// the file or the environment answers for it.
//
// An OBSERVING lane is refused too, although it would file nothing either
// way. Observe is a rehearsal for enforcing, and this config can never be
// enforced: a process with no plane also has no reloader, so the lane cannot
// leave observe without a restart that hits this same refusal. Accepting it
// would let an operator rehearse a control that does not exist.
func holdsWithoutAPlane(cfg *Config, la *LaneAnalyzerConfig, ac *analyzerDeps) bool {
	if !analyzerHolds(la) {
		return false
	}
	if ac != nil && ac.cp != nil {
		return false
	}
	return !cfg.controlPlaneConfigured()
}

// fileReview runs one POST /api/sidecars/reviews.
//
// ctx carries the lane's analyzer timeout, so the hold is bounded by the same
// number that bounds the model call, and the client's socket waits for at
// most the two of them. controlPlaneHTTPClient's own timeout is the backstop
// for a caller that passed none.
//
// Every failure returns an error, and the caller denies on one. There is no
// retry: the client retries by running the statement again, and a second
// attempt from inside one evaluation would double the time a connection is
// held to learn the same thing.
func (cp *controlPlane) fileReview(ctx context.Context, listener, rule, statement string) (analyzer.ReviewResult, error) {
	var out analyzer.ReviewResult

	// The statement is base64 because it is bytes, not text: a wire
	// statement can carry anything the client typed, and the plane hashes
	// exactly what arrives to match a retry against the approval.
	body, err := json.Marshal(map[string]string{
		"listener_name": listener,
		"approval_rule": rule,
		"payload":       base64.StdEncoding.EncodeToString([]byte(statement)),
	})
	if err != nil {
		return out, fmt.Errorf("encoding the review request: %w", err)
	}

	// The base was validated by checkControlPlaneURL; JoinPath keeps a
	// path prefix (a plane behind /hoop) and normalizes trailing slashes.
	u, err := url.Parse(cp.url)
	if err != nil {
		return out, fmt.Errorf("control plane URL %q: %w", cp.url, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		u.JoinPath(controlPlaneReviewsPath).String(), bytes.NewReader(body))
	if err != nil {
		return out, fmt.Errorf("control plane request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(sidecarTokenHeader, cp.token)

	resp, err := controlPlaneHTTPClient().Do(req)
	if err != nil {
		return out, fmt.Errorf("the control plane at %s is unreachable: %w", cp.url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Read one byte past the bound, so an oversized answer is reported as
	// what it is rather than decoded from a truncated document and blamed
	// on bad JSON. Same move the handshake makes.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxReviewResponse+1))
	if err != nil {
		return out, fmt.Errorf("reading the review answer from %s: %w", cp.url, err)
	}
	if len(raw) > maxReviewResponse {
		return out, fmt.Errorf("the control plane at %s answered a review with more than "+
			"%d bytes; check that the URL is the control plane and not something in "+
			"front of it", cp.url, maxReviewResponse)
	}

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return decodeReview(raw)
	case http.StatusUnauthorized:
		return out, fmt.Errorf("the control plane at %s rejected the token", cp.url)
	case http.StatusPreconditionFailed:
		// The gateway authenticated the token and does not serve
		// reviews: it is running as a gateway, not as a control plane.
		// Startup cannot catch this, because the handshake works.
		return out, fmt.Errorf("the gateway at %s does not serve sidecar reviews; "+
			"require_review needs a control plane: %s", cp.url, controlPlaneMessage(raw))
	case http.StatusRequestEntityTooLarge:
		return out, fmt.Errorf("the control plane at %s refused the statement as too "+
			"large to review: %s", cp.url, controlPlaneMessage(raw))
	case http.StatusUnprocessableEntity:
		// The plane authorizes each review against the configuration it
		// stored for this sidecar, so a listener whose approval_rule was
		// edited locally, or a rule with no reviewers, lands here.
		return out, fmt.Errorf("the control plane at %s refused a review for listener %q "+
			"under rule %q: %s", cp.url, listener, rule, controlPlaneMessage(raw))
	}

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// Same reasoning as the handshake: the token rides a custom
		// header that Go would forward across origins.
		return out, fmt.Errorf("the control plane at %s redirected to %q; the review "+
			"never follows one, so the token was not re-sent. Configure the final URL",
			cp.url, resp.Header.Get("Location"))
	}
	return out, fmt.Errorf("the control plane at %s answered %s: %s",
		cp.url, resp.Status, controlPlaneMessage(raw))
}

// decodeReview reads the answer.
//
// Only three fields are read of the review document the plane returns, and
// the rest is ignored on purpose: the reviewer groups, the approval count and
// the timestamps belong to whoever is deciding, and a sidecar that parsed
// them would be a second reader of a policy it does not enforce.
func decodeReview(raw []byte) (analyzer.ReviewResult, error) {
	var answer struct {
		Forward bool `json:"forward"`
		Review  *struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"review"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return analyzer.ReviewResult{}, fmt.Errorf("the review answer could not be read: %w", err)
	}
	out := analyzer.ReviewResult{Forward: answer.Forward}
	if answer.Review != nil {
		out.ID = answer.Review.ID
		out.Status = answer.Review.Status
	}
	return out, nil
}
