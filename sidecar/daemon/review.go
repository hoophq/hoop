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
	"strconv"
	"strings"

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

// LocalReviewer returns the backend one lane files held statements with when
// the process has no control plane: the person running it, at its terminal.
//
// The listener names the lane, so one reviewer can tell an operator which
// database a statement is waiting at. It never receives an approval_rule:
// that names a rule stored in a control plane, and there is none to ask.
type LocalReviewer func(listener string) analyzer.Reviewer

// WithLocalReviewer lets a process without a control plane hold statements
// for review: require_review files with r instead of refusing to start, and
// such a lane needs no approval_rule.
//
// For an entry point that has a person in front of it, which is why only
// `hoop start sidecar` on a terminal passes it. A control plane, when one is
// configured, always wins: its rule decides who approves, and a local
// reviewer never releases a statement the plane would have to authorize.
func WithLocalReviewer(r LocalReviewer) Option {
	return func(o *setupOptions) { o.localReviewer = r }
}

// holdRefusal is why a lane that holds statements cannot be built, or "".
//
// Two refusals, both at build rather than in Config.Validate for the reason
// holdsWithoutAPlane gives. With a control plane, require_review needs an
// approval_rule: the plane refuses a review that names none. Without one, it
// needs a local reviewer, or there is nowhere to file the review at all.
func holdRefusal(cfg *Config, la *LaneAnalyzerConfig, ac *analyzerDeps) string {
	if !analyzerHolds(la) {
		return ""
	}
	if holdsWithoutAPlane(cfg, la, ac) {
		if ac != nil && ac.local != nil {
			return ""
		}
		return fmt.Sprintf("the analyzer block asks for %s and this sidecar has no control "+
			"plane; the approval request is filed with the plane named by %s or the "+
			"control_plane_url key, or with the person running `hoop start sidecar` "+
			"in a terminal, and there is neither",
			holdAction, ControlPlaneURLEnv)
	}
	if la.ApprovalRule == "" {
		return fmt.Sprintf("the analyzer block asks for %s and names no approval_rule; the "+
			"rule is what decides who may approve a held statement, and the control "+
			"plane refuses an approval request that does not name one", holdAction)
	}
	return ""
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
		return out, fmt.Errorf("encoding the approval request: %w", err)
	}

	resp, raw, err := cp.reviewRequest(ctx, http.MethodPost, body, controlPlaneReviewsPath)
	if err != nil {
		return out, err
	}
	switch resp.StatusCode {
	case http.StatusRequestEntityTooLarge:
		return out, fmt.Errorf("the control plane at %s refused the statement as too "+
			"large for an approval request: %s", cp.url, controlPlaneMessage(raw))
	case http.StatusUnprocessableEntity:
		// The plane authorizes each review against the configuration it
		// stored for this sidecar, so a listener whose approval_rule was
		// edited locally, or a rule with no reviewers, lands here.
		return out, fmt.Errorf("the control plane at %s refused an approval request for listener %q "+
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
	resp, raw, err := cp.reviewRequest(ctx, http.MethodPost, nil,
		controlPlaneReviewsPath, url.PathEscape(reviewID), controlPlaneClaimPath)
	if err != nil {
		return out, err
	}
	if resp.StatusCode == http.StatusNotFound {
		// Also what a control plane older than this sidecar answers: it
		// has no claim route. The hold denies either way, and the retry
		// path still works against it.
		return out, fmt.Errorf("the control plane at %s has no approval request %s to claim; "+
			"a control plane older than this sidecar cannot answer a waiting hold: %s",
			cp.url, reviewID, controlPlaneMessage(raw))
	}
	out, err = cp.reviewAnswer(resp, raw)
	if err != nil {
		return analyzer.ReviewResult{}, err
	}
	if out.ID != reviewID {
		return analyzer.ReviewResult{}, fmt.Errorf(
			"the control plane answered about approval %q when asked about %q", out.ID, reviewID)
	}
	return out, nil
}

// reviewRequest sends one request under the reviews path and reads the
// answer, bounded. The status is the caller's to interpret: reviewAnswer
// handles what every review call shares.
func (cp *controlPlane) reviewRequest(ctx context.Context, method string, body []byte, elem ...string) (*http.Response, []byte, error) {
	return cp.reviewRoundTrip(ctx, method, body, nil, elem...)
}

// reviewQuery is a GET under the reviews path with a query string.
func (cp *controlPlane) reviewQuery(ctx context.Context, query url.Values, elem ...string) (*http.Response, []byte, error) {
	return cp.reviewRoundTrip(ctx, http.MethodGet, nil, query, elem...)
}

func (cp *controlPlane) reviewRoundTrip(ctx context.Context, method string, body []byte, query url.Values, elem ...string) (*http.Response, []byte, error) {
	// The base was validated by checkControlPlaneURL; JoinPath keeps a
	// path prefix (a plane behind /hoop) and normalizes trailing slashes.
	u, err := url.Parse(cp.url)
	if err != nil {
		return nil, nil, fmt.Errorf("control plane URL %q: %w", cp.url, err)
	}
	u = u.JoinPath(elem...)
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("control plane request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// ctx bounds a metadata server fetch too: refreshing a Google ID token
	// is part of this review call's budget, not a second one.
	header, value, err := cp.cred.present(ctx)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set(header, value)

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
		return nil, nil, fmt.Errorf("reading the approval answer from %s: %w", cp.url, err)
	}
	if len(raw) > maxReviewResponse {
		return nil, nil, fmt.Errorf("the control plane at %s answered an approval request with more than "+
			"%d bytes; check that the URL is the control plane and not something in "+
			"front of it", cp.url, maxReviewResponse)
	}
	// Answered here rather than in reviewError, because only this function
	// still holds the identity the plane refused, and the message names it.
	// The token path keeps reviewError's wording.
	if resp.StatusCode == http.StatusUnauthorized && header == SidecarIdentityHeader {
		return nil, nil, identityRejected(cp.url, value, raw)
	}
	return resp, raw, nil
}

// ReviewStatus implements ReviewStatusReader with one
// GET /api/sidecars/reviews/<id>, the read-only route. It never calls claim:
// claim spends the approval, and the agent's resend would then read EXECUTED.
func (cp *controlPlane) ReviewStatus(ctx context.Context, reviewID string) (ReviewStatus, error) {
	// The plane issues canonical UUIDs and answers anything else as not
	// found. Checking here keeps an id like ".." or "a/b" off the wire: the
	// URL would miss the route, and its plain 404 would read as an old plane.
	reviewID = strings.ToLower(reviewID)
	if !canonicalUUID(reviewID) {
		return ReviewStatus{}, ErrReviewNotFound
	}
	resp, raw, err := cp.reviewRequest(ctx, http.MethodGet, nil,
		controlPlaneReviewsPath, url.PathEscape(reviewID))
	if err != nil {
		return ReviewStatus{}, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		var out ReviewStatus
		if err := json.Unmarshal(raw, &out); err != nil {
			return ReviewStatus{}, fmt.Errorf("the approval status could not be read: %w", err)
		}
		if out.ID != reviewID {
			return ReviewStatus{}, fmt.Errorf(
				"the control plane answered about approval %q when asked about %q", out.ID, reviewID)
		}
		return out, nil
	case http.StatusNotFound:
		if planeNotFound(raw) {
			return ReviewStatus{}, ErrReviewNotFound
		}
		return ReviewStatus{}, ErrPlaneTooOld
	}
	return ReviewStatus{}, cp.reviewError(resp, raw)
}

// ListReviews implements ReviewStatusReader with one GET /api/sidecars/reviews,
// read-only like ReviewStatus. The plane scopes the list to this sidecar's
// token, which is the whole of its authorization: every listener and replica
// sharing the token sees the same list.
func (cp *controlPlane) ListReviews(ctx context.Context, status string, limit int) ([]ReviewStatus, error) {
	if limit < 1 || limit > MaxReviewListLimit {
		return nil, fmt.Errorf("limit must be from 1 to %d", MaxReviewListLimit)
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if status != "" {
		q.Set("status", status)
	}
	resp, raw, err := cp.reviewQuery(ctx, q, controlPlaneReviewsPath)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		var out []ReviewStatus
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("the approval list could not be read: %w", err)
		}
		return out, nil
	case http.StatusNotFound:
		// The route never answers 404; Gin's own does, on a plane older
		// than the route.
		return nil, ErrPlaneTooOld
	case http.StatusUnauthorized, http.StatusForbidden:
		// An older plane routes this GET to the admin GET /sidecars/:name,
		// which refuses a sidecar token. A wrong token reads the same.
		return nil, fmt.Errorf("%w, or it rejected the token", ErrPlaneTooOld)
	case http.StatusBadRequest:
		return nil, fmt.Errorf("the control plane refused the list: %s", controlPlaneMessage(raw))
	}
	return nil, cp.reviewError(resp, raw)
}

// canonicalUUID reports a lowercase 8-4-4-4-12 hex UUID, the only form the
// plane issues.
func canonicalUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
	}
	return true
}

// planeNotFound tells the status route's own 404 from a plane that has no
// such route. The route answers {"message": ...}; an older gateway falls
// through to Gin's plain-text "404 page not found". The gateway's
// reviews_test.go pins the JSON body, so this does not rest on a comment.
func planeNotFound(raw []byte) bool {
	var m struct {
		Message *string `json:"message"`
	}
	return json.Unmarshal(raw, &m) == nil && m.Message != nil
}

// reviewAnswer interprets the statuses both review calls share.
func (cp *controlPlane) reviewAnswer(resp *http.Response, raw []byte) (analyzer.ReviewResult, error) {
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		return decodeReview(raw)
	}
	return analyzer.ReviewResult{}, cp.reviewError(resp, raw)
}

// reviewError reports an answer no review call accepts.
func (cp *controlPlane) reviewError(resp *http.Response, raw []byte) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("the control plane at %s rejected the token", cp.url)
	case http.StatusPreconditionFailed:
		// The gateway authenticated the token and does not serve
		// reviews: it is running as a gateway, not as a control plane.
		// Startup cannot catch this, because the handshake works.
		return fmt.Errorf("the gateway at %s does not serve sidecar approvals; "+
			"require_approval needs a control plane: %s", cp.url, controlPlaneMessage(raw))
	}

	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		// Same reasoning as the handshake: the token rides a custom
		// header that Go would forward across origins.
		return fmt.Errorf("the control plane at %s redirected to %q; the approval request "+
			"never follows one, so the token was not re-sent. Configure the final URL",
			cp.url, resp.Header.Get("Location"))
	}
	return fmt.Errorf("the control plane at %s answered %s: %s",
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
		return analyzer.ReviewResult{}, fmt.Errorf("the approval answer could not be read: %w", err)
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
			"the control plane released a statement without naming an approval")
	}
	return out, nil
}
