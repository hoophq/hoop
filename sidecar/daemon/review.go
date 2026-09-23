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
// once. It is the FIRST ask for a statement and never the ones after it: see
// controlPlaneClaimPath.
const controlPlaneReviewsPath = "/api/sidecars/reviews"

// controlPlaneClaimPath is where a waiting hold asks about the review it was
// given, under controlPlaneReviewsPath as <id>/claim.
//
// It answers about that one review and never files. Asking the filing path
// again would, once another connection spent the approval: the plane matches
// live reviews only, so it would file a fresh one and page the approvers
// from inside a wait nobody started.
const controlPlaneClaimPath = "claim"

// maxReviewResponse bounds the response read. A review answer is a few
// hundred bytes; anything near this is not the control plane.
const maxReviewResponse = 1 << 20

// planReviewer is one lane's analyzer.Reviewer, with the two facts the plane
// authorizes a filing against bound in: the listener the statement arrived on
// and the approval rule that lane named.
//
// Both are per lane, and neither is on analyzer.Config: the evaluator never
// reads them, it would only copy them into a request body, and the listener
// is NOT the evaluator's own name: a deprecated ai_analysis rule carries an
// operator-chosen one. Binding them here keeps the analyzer package unable to
// get that wrong.
type planReviewer struct {
	cp       *controlPlane
	listener string
	rule     string
}

// File implements analyzer.Reviewer.
func (r planReviewer) File(ctx context.Context, statement string) (analyzer.ReviewResult, error) {
	return r.cp.fileReview(ctx, r.listener, r.rule, statement)
}

// Claim implements analyzer.Reviewer.
func (r planReviewer) Claim(ctx context.Context, reviewID string) (analyzer.ReviewResult, error) {
	return r.cp.claimReview(ctx, reviewID)
}

// reviewer returns the backend one lane holds statements against.
//
// A nil control plane returns a nil Reviewer, which denies at evaluation time.
// That is the -validate path: it builds every lane without ever contacting a
// plane, so a client cannot exist there, and a build error would refuse a
// config that runs correctly. The nil is untyped on purpose: a nil
// *controlPlane inside a planReviewer would read as a backend.
func (cp *controlPlane) reviewer(listener, rule string) analyzer.Reviewer {
	if cp == nil {
		return nil
	}
	return planReviewer{cp: cp, listener: listener, rule: rule}
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
// ctx carries the lane's analyzer timeout, so each call is bounded by the same
// number that bounds the model call. controlPlaneHTTPClient's own timeout is
// the backstop for a caller that passed none.
//
// Every failure returns an error, and the caller denies on one. There is no
// retry here: a hold that is still pending asks again through claimReview.
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

	resp, raw, err := cp.postReview(ctx, body, controlPlaneReviewsPath)
	if err != nil {
		return out, err
	}
	switch resp.StatusCode {
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
	return cp.reviewAnswer(resp, raw)
}

// claimReview runs one POST /api/sidecars/reviews/<id>/claim: what a waiting
// hold asks about the review fileReview named.
//
// The answer must be about that review. One naming another is not the plane
// answering this question, and a release on it would tie the statement to a
// decision nobody made about it.
func (cp *controlPlane) claimReview(ctx context.Context, reviewID string) (analyzer.ReviewResult, error) {
	var out analyzer.ReviewResult

	// Escaped, so an id is always one path segment whatever it carries.
	resp, raw, err := cp.postReview(ctx, nil,
		controlPlaneReviewsPath, url.PathEscape(reviewID), controlPlaneClaimPath)
	if err != nil {
		return out, err
	}
	if resp.StatusCode == http.StatusNotFound {
		// Also what a control plane older than this sidecar answers: it
		// has no claim route. The hold denies either way, and the retry
		// path still works against it.
		return out, fmt.Errorf("the control plane at %s has no review %s to claim; "+
			"a control plane older than this sidecar cannot answer a waiting hold: %s",
			cp.url, reviewID, controlPlaneMessage(raw))
	}
	out, err = cp.reviewAnswer(resp, raw)
	if err != nil {
		return analyzer.ReviewResult{}, err
	}
	if out.ID != reviewID {
		return analyzer.ReviewResult{}, fmt.Errorf(
			"the control plane answered about review %q when asked about %q", out.ID, reviewID)
	}
	return out, nil
}

// postReview sends one request under the reviews path and reads the answer,
// bounded. The status is the caller's to interpret: reviewAnswer handles what
// both review calls share.
func (cp *controlPlane) postReview(ctx context.Context, body []byte, elem ...string) (*http.Response, []byte, error) {
	// The base was validated by checkControlPlaneURL; JoinPath keeps a
	// path prefix (a plane behind /hoop) and normalizes trailing slashes.
	u, err := url.Parse(cp.url)
	if err != nil {
		return nil, nil, fmt.Errorf("control plane URL %q: %w", cp.url, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		u.JoinPath(elem...).String(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("control plane request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(sidecarTokenHeader, cp.token)

	resp, err := controlPlaneHTTPClient().Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("the control plane at %s is unreachable: %w", cp.url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Read one byte past the bound, so an oversized answer is reported as
	// what it is rather than decoded from a truncated document and blamed
	// on bad JSON. Same move the handshake makes.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxReviewResponse+1))
	if err != nil {
		return nil, nil, fmt.Errorf("reading the review answer from %s: %w", cp.url, err)
	}
	if len(raw) > maxReviewResponse {
		return nil, nil, fmt.Errorf("the control plane at %s answered a review with more than "+
			"%d bytes; check that the URL is the control plane and not something in "+
			"front of it", cp.url, maxReviewResponse)
	}
	return resp, raw, nil
}

// reviewAnswer interprets the statuses both review calls share.
func (cp *controlPlane) reviewAnswer(resp *http.Response, raw []byte) (analyzer.ReviewResult, error) {
	var out analyzer.ReviewResult
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
	// A release has to name the review it spent. The plane always does, so
	// anything answering `{"forward":true}` and nothing else is not the
	// plane: a proxy, a captive portal, a URL pointing at the wrong service.
	// Releasing on one JSON field would put a statement through on a
	// document nobody authorized and leave an audit record tying it to no
	// human decision, which is the one record this feature exists to write.
	if out.Forward && out.ID == "" {
		return analyzer.ReviewResult{}, fmt.Errorf(
			"the control plane released a statement without naming a review")
	}
	return out, nil
}
