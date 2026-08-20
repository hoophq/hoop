// Package inspectapi is hoop-inspect's control plane: what the relay, running
// outside this process, asks the gateway for.
//
// Today it holds one feature — the statement-level human-approval gate — and
// it is where config distribution and event shipping would go.
//
// Every route is machine-only, and the middleware that enforces that is on the
// GROUP in gateway/api/server.go rather than on each route. A route added here
// later is therefore machine-only by construction, instead of by whoever adds
// it remembering.
//
// Three endpoints, all reached with an hpk_ credential — an API key or an AI
// agent, both of which resolve to a context carrying groups:
//
//   - POST /inspect/reviews/claim — the relay's AUTHORIZATION step, on the
//     data path. Atomically consumes an approved review for one exact
//     statement.
//   - POST /inspect/reviews — the relay's find-or-create, also on the data
//     path. Files the review a human answers.
//   - GET  /inspect/reviews — the sandbox agent's status poll, off the data
//     path.
//
// # Scoping comes from the credential, never from the body
//
// Every handler derives org and owner from the authenticated token and
// resolves the connection through the access-control-aware lookup. Nothing a
// caller writes in a request body can widen what it reaches: the connection
// name is a lookup that fails when the caller has no access, and org and owner
// are never read from the request at all.
//
// The hpk_ token is bound to a named sandbox environment rather than to a
// human it acts for, so the sandbox OWNS its reviews: OwnerID and OwnerEmail
// on the review are the sandbox's, and a reviewer reading the queue sees which
// environment asked. Cross-sandbox reuse is unreachable rather than defended
// against — two sandboxes hold different credentials with no access to each
// other's reviews — which leaves exactly one threat for the statement hash to
// address: a sandbox reusing its OWN approval behind different SQL.
package inspectapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/openapi"
	sessionapi "github.com/hoophq/hoop/gateway/api/session"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/events"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"gorm.io/gorm"
)

// maxStatementBytes bounds the statement text a review can carry.
//
// It is the text a human reads in the review queue, and it lands in a blob
// row. The relay's own per-message ceiling is far higher, so a statement above
// this is refused rather than truncated: approving a statement that was cut
// short would authorize a hash covering text the reviewer never saw.
const maxStatementBytes = 64 << 10

// maxMarkerLen mirrors the relay's own marker bound (review.MaxMarkerLen).
// Kept as a plain constant rather than an import: the gateway must not depend
// on the relay's module to validate its own input, and the only cost of the
// two drifting apart is a request the gateway refuses.
const maxMarkerLen = 128

// Claim
//
//	@Summary		Claim an inspect review
//	@Description	Atomically consume the caller's approved review for one exact statement and report it as EXECUTED. An approval authorizes exactly one execution, so a second call finds nothing. Returns 404 when there is no approved, unconsumed review — which is also the answer for a PENDING, REJECTED or REVOKED one.
//	@Tags				Inspect
//	@Accept			json
//	@Produce		json
//	@Param			request			body		openapi.InspectReviewClaimRequest	true	"The request body resource"
//	@Success		200				{object}	openapi.InspectReview
//	@Failure		400,401,404,500	{object}	openapi.HTTPError
//	@Router			/inspect/reviews/claim [post]
func Claim(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	var req openapi.InspectReviewClaimRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if !validStatementHash(req.StatementHash) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "statement_hash must be 64 lowercase hex characters"})
		return
	}
	conn, ok := resolveConnection(c, ctx, req.Connection)
	if !ok {
		return
	}

	rev, err := services.ClaimInspectReview(ctx.OrgID, ctx.UserID, conn.ID, req.StatementHash)
	switch {
	case errors.Is(err, services.ErrNoApprovedReview):
		c.JSON(http.StatusNotFound, gin.H{"message": services.ErrNoApprovedReview.Error()})
		return
	case err != nil:
		log.With("org", ctx.OrgID, "owner", ctx.UserID, "conn", conn.Name).
			Errorf("failed claiming inspect review: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed claiming review"})
		return
	}

	log.With("org", ctx.OrgID, "owner", ctx.UserID, "conn", conn.Name,
		"review-id", rev.ID, "sid", rev.SessionID).Infof("inspect review claimed")

	c.JSON(http.StatusOK, toOpenAPI(rev))
}

// Create
//
//	@Summary		File an inspect review
//	@Description	Find or create a review for one statement. When the request carries a marker and this sandbox already has a PENDING review for the same connection and marker, that review is returned with 200 instead of a duplicate being filed. A unique index enforces that, so two concurrent requests under one marker also collapse to one review and the loser receives it with 200. Otherwise a session and a one-time review are created and 201 is returned.
//	@Tags				Inspect
//	@Accept			json
//	@Produce		json
//	@Param			request					body		openapi.InspectReviewRequest	true	"The request body resource"
//	@Success		200,201					{object}	openapi.InspectReview
//	@Failure		400,401,404,409,422,500	{object}	openapi.HTTPError
//	@Router			/inspect/reviews [post]
func Create(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	var req openapi.InspectReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	if !validStatementHash(req.StatementHash) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "statement_hash must be 64 lowercase hex characters"})
		return
	}
	if strings.TrimSpace(req.Statement) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "statement is empty; a reviewer has to have something to read"})
		return
	}
	if len(req.Statement) > maxStatementBytes {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": fmt.Sprintf("statement is larger than %d bytes", maxStatementBytes)})
		return
	}
	if len(req.Marker) > maxMarkerLen {
		c.JSON(http.StatusBadRequest, gin.H{
			"message": fmt.Sprintf("marker is longer than %d characters", maxMarkerLen)})
		return
	}

	conn, ok := resolveConnection(c, ctx, req.Connection)
	if !ok {
		return
	}
	orgID, err := uuid.Parse(ctx.GetOrgID())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "invalid organization id"})
		return
	}

	// Find, then create. The claim filters on APPROVED, so a retry issued
	// before a human has looked at the queue does not see its own PENDING
	// review; without this a polling agent files one review per attempt.
	//
	// Keyed on the MARKER rather than the hash, because only the marker knows
	// the caller's intent: an agent whose task 3 and task 9 run byte-identical
	// SQL is making two requests, and each still needs its own human.
	existing, err := services.FindPendingInspectReviewByMarker(ctx.OrgID, ctx.UserID, conn.ID, req.Marker)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, toOpenAPI(existing))
		return
	case !errors.Is(err, services.ErrNoApprovedReview):
		log.With("org", ctx.OrgID, "conn", conn.Name).
			Errorf("failed looking up pending inspect review: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed looking up review"})
		return
	}

	// Who reviews this is the org's existing access-request configuration,
	// not a second approval system. A connection with no rule, or a rule with
	// no reviewers, is a config gap and must surface as an error: answering
	// "approved" or forwarding silently would both be worse than refusing.
	accessRule, err := services.GetRuleForConnection(orgID, conn.Name, models.AccessTypeCommand)
	if err != nil {
		log.With("org", ctx.OrgID, "conn", conn.Name).
			Errorf("failed resolving access request rule: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed resolving access request rule"})
		return
	}
	if accessRule == nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf(
			"connection %q has no access request rule, so there is nobody to review this statement", conn.Name)})
		return
	}
	// Checked HERE, before anything is written. The same precondition is
	// enforced inside CreateReviewFromAIAnalysis, but reaching it there would
	// mean the session below already exists — an open session anchoring a
	// review that was never filed, stranded in the session list forever.
	//
	// It is also a 422 rather than a 500: a rule with no reviewers_groups is
	// the same class of config gap as no rule at all, and the caller can act
	// on it.
	if err := sessionapi.ValidateAccessRuleForReview(accessRule); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf(
			"connection %q cannot be reviewed: %v", conn.Name, err)})
		return
	}

	// One session per review: private.reviews carries UNIQUE(org_id,
	// session_id), and the session is what a reviewer opens to read the
	// statement. It stays open until the review is answered, exactly as the
	// AI-analysis path already leaves it.
	sessionID := uuid.NewString()
	analysis := analysisFor(req)
	session := models.Session{
		ID:                sessionID,
		OrgID:             ctx.OrgID,
		Connection:        conn.Name,
		ConnectionType:    conn.Type,
		ConnectionSubtype: conn.SubType.String,
		Verb:              pb.ClientVerbExec,
		Origin:            pb.SessionOriginInspect,
		IdentityType:      "machine",
		UserID:            ctx.UserID,
		UserName:          ctx.UserName,
		UserEmail:         ctx.UserEmail,
		BlobInput:         models.BlobInputType(req.Statement),
		Status:            "open",
		CreatedAt:         time.Now().UTC(),
		// Attached to the session, not only to the Slack message, so the
		// reviewer opening the webapp sees the risk that got the statement
		// held. Same field the AI-analysis path sets for the same reason.
		AIAnalysis: analysis,
	}
	if req.Marker != "" {
		session.CorrelationID = &req.Marker
	}
	if err := models.UpsertSession(session); err != nil {
		log.With("org", ctx.OrgID, "sid", sessionID).Errorf("failed persisting session for an inspect review: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed creating review session"})
		return
	}

	rev, err := sessionapi.CreateReviewFromAIAnalysis(orgID, sessionID, conn,
		sessionapi.AIReviewRequester{
			UserID:      ctx.UserID,
			UserEmail:   ctx.UserEmail,
			UserName:    ctx.UserName,
			UserSlackID: ctx.SlackID,
			UserGroups:  ctx.UserGroups,
		},
		accessRule, req.Statement, nil, nil, analysis,
		&sessionapi.ReviewStatementKeys{
			StatementHash: req.StatementHash,
			RequestMarker: req.Marker,
		})
	if err != nil {
		// The session exists only to anchor this review. With no review to
		// anchor it is an open row nobody will ever close, so it is removed
		// rather than left behind — on the losing-race path below just as
		// much as on a genuine failure.
		//
		// Best effort: a genuine failure here is usually the database being
		// unhealthy, in which case this fails too. Logged and swallowed,
		// because the caller's answer is the same either way and a cleanup
		// failure must not turn one response into a different one.
		cleanup := func() {
			if delErr := models.DeleteSessionWithInput(ctx.OrgID, sessionID); delErr != nil {
				log.With("org", ctx.OrgID, "sid", sessionID).
					Warnf("failed removing the session of a review that was never created: %v", delErr)
			}
		}

		// Lost the dedupe race. Two concurrent retries under one marker can
		// both find no PENDING review and both try to file one; the unique
		// partial index on (org, owner, connection, request_marker) lets
		// exactly one win. This is the loser, and the right answer is the
		// winner's review — the same 200 a sequential retry would have got.
		//
		// Not an error path in any sense the caller cares about: the caller
		// asked for a review of this request and there is one.
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			cleanup()
			existing, lookupErr := services.FindPendingInspectReviewByMarker(
				ctx.OrgID, ctx.UserID, conn.ID, req.Marker)
			if lookupErr == nil {
				log.With("org", ctx.OrgID, "conn", conn.Name, "marker", req.Marker,
					"review-id", existing.ID).Infof("lost the inspect review dedupe race; returning the winner")
				c.JSON(http.StatusOK, toOpenAPI(existing))
				return
			}
			// The winner was answered and left the partial index between the
			// insert and this read. Rare, and the caller should just retry:
			// the next attempt files cleanly.
			log.With("org", ctx.OrgID, "conn", conn.Name, "marker", req.Marker).
				Warnf("lost the inspect review dedupe race and the winner was gone: %v", lookupErr)
			c.JSON(http.StatusConflict, gin.H{
				"message": "a review for this marker was filed concurrently and has already been answered; retry"})
			return
		}

		log.With("org", ctx.OrgID, "sid", sessionID).Errorf("failed creating inspect review: %v", err)
		cleanup()
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed creating review"})
		return
	}

	events.DeriveFromSessionStart(ctx.OrgID, &session, conn)

	c.JSON(http.StatusCreated, openapi.InspectReview{
		ReviewID:  rev.ID,
		SessionID: rev.SessionID,
		Status:    string(rev.Status),
		URL:       sessionURL(rev.SessionID),
	})
}

// Get
//
//	@Summary		Poll an inspect review
//	@Description	Report where the caller's review for one statement stands. Read-only: polling never consumes an approval, which only the relay's claim may do. An APPROVED review wins over a newer one in any other status, because the question being asked is whether the statement may be retried yet.
//	@Tags				Inspect
//	@Produce		json
//	@Param			connection		query		string	true	"The connection the statement runs against"
//	@Param			statement_hash	query		string	true	"SHA-256 of the canonical statement text, 64 lowercase hex characters"
//	@Success		200				{object}	openapi.InspectReview
//	@Failure		400,401,404,429,500	{object}	openapi.HTTPError
//	@Router			/inspect/reviews [get]
func Get(c *gin.Context) {
	ctx := storagev2.ParseContext(c)

	statementHash := c.Query("statement_hash")
	if !validStatementHash(statementHash) {
		c.JSON(http.StatusBadRequest, gin.H{"message": "statement_hash must be 64 lowercase hex characters"})
		return
	}
	conn, ok := resolveConnection(c, ctx, c.Query("connection"))
	if !ok {
		return
	}

	rev, err := services.GetInspectReviewStatus(ctx.OrgID, ctx.UserID, conn.ID, statementHash)
	switch {
	case errors.Is(err, services.ErrNoApprovedReview):
		c.JSON(http.StatusNotFound, gin.H{"message": "no review for this statement"})
		return
	case err != nil:
		log.With("org", ctx.OrgID, "conn", conn.Name).Errorf("failed reading inspect review: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed reading review"})
		return
	}
	c.JSON(http.StatusOK, toOpenAPI(rev))
}

// resolveConnection looks the connection up under the caller's own access.
//
// This is where authorization for the whole feature lives: the lookup is
// access-control aware, so a sandbox asking about a connection its groups do
// not reach gets the same answer as one asking about a connection that does
// not exist. The review gate composes with that; it does not replace it.
func resolveConnection(c *gin.Context, ctx *storagev2.Context, nameOrID string) (*models.Connection, bool) {
	if strings.TrimSpace(nameOrID) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"message": "connection is required"})
		return nil, false
	}
	conn, err := models.GetConnectionByNameOrID(ctx, nameOrID)
	switch {
	case err != nil && !errors.Is(err, models.ErrNotFound):
		log.With("org", ctx.OrgID).Errorf("failed fetching connection %q: %v", nameOrID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"message": "failed fetching connection"})
		return nil, false
	case conn == nil || errors.Is(err, models.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "connection not found"})
		return nil, false
	}
	return conn, true
}

// analysisFor renders the analyzer's verdict for the reviewer, or nil when the
// caller reported none.
//
// Classifications only. The relay never sends the model's prose, because a
// model that quotes the statement back would publish its literals into a
// notification channel.
func analysisFor(req openapi.InspectReviewRequest) *models.SessionAIAnalysis {
	if req.RiskLevel == "" && req.Rule == "" {
		return nil
	}
	title := "Statement held for review"
	if req.Rule != "" {
		title = fmt.Sprintf("Statement held for review by rule %q", req.Rule)
	}
	return &models.SessionAIAnalysis{
		RiskLevel: req.RiskLevel,
		Title:     title,
		Summary:   "hoop-inspect flagged this statement and is holding it until a human approves it.",
	}
}

func toOpenAPI(r *models.InspectReview) openapi.InspectReview {
	return openapi.InspectReview{
		ReviewID:  r.ID,
		SessionID: r.SessionID,
		Status:    string(r.Status),
		URL:       sessionURL(r.SessionID),
	}
}

func sessionURL(sessionID string) string {
	return fmt.Sprintf("%s/sessions/%s", appconfig.Get().FullApiURL(), sessionID)
}

// validStatementHash enforces the exact shape the relay produces.
//
// Lowercase hex of a fixed length, so a caller cannot smuggle a wildcard, a
// SQL fragment or an unbounded string into an indexed lookup column, and so a
// key that does not match is a visible 400 rather than a silent miss that
// looks like "no approval".
func validStatementHash(v string) bool {
	if len(v) != 64 {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
