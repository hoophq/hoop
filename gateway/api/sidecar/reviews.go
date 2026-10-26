package apisidecar

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aws/smithy-go/ptr"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/analytics"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	slackplugin "github.com/hoophq/hoop/gateway/transport/plugins/slack"
	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

const (
	// reviewOwnerEmail stands in for the requester a sidecar review does not
	// have. The control plane registers no ordinary users, so nothing here
	// identifies a person. Known debt: every sidecar review shows the same
	// requester until there is something real to record, which is where the
	// database user or the source address behind the statement will go.
	reviewOwnerEmail = "hoop@hoop.dev"

	// maxStatementBytes caps the decoded statement. It is stored twice, as the
	// session blob and the review blob, and a token holder could otherwise fill
	// the database one request at a time. A statement a human is expected to
	// read has no business being larger.
	maxStatementBytes = 100000 // 0.1MB, the ceiling plugin config already uses

	// reviewSlackType labels the review in Slack, where the message renders a
	// "Type" line for what a gateway review calls its connection type.
	reviewSlackType = "sidecar"

	// reviewConnectionType is what a session must declare. private.sessions
	// requires one and enum_connection_type has no label for a listener, so a
	// sidecar session takes the catch-all rather than the enum taking a new
	// value it would carry forever.
	reviewConnectionType = "custom"
)

// ruleNotAuthorized answers every way the named rule can fail to authorize the
// review: no such rule, a rule of another kind, a listener the stored
// configuration does not have, and a listener that names a different rule. One
// message for the four, because telling them apart would let a token holder
// enumerate the organization's rule names.
//
// A type rather than a wrapped sentinel, so what the sidecar reads is the
// whole message with no internal marker appended to it.
//
// Both values came from the caller, so naming them leaks nothing and makes the
// window after a configuration edit readable in the sidecar's own log: a
// review is refused until the sidecar reloads, up to a minute, and the sidecar
// denies the statement in the meantime.
type ruleNotAuthorized struct{ listenerName, ruleName string }

func (e ruleNotAuthorized) Error() string {
	return fmt.Sprintf("listener %q is not configured to use approval rule %q", e.listenerName, e.ruleName)
}

// levelNotAuthorized is ruleNotAuthorized for a filing under a risk level:
// one message for a listener the stored configuration does not have, a level
// that does not hold, and a level held for another approver.
type levelNotAuthorized struct{ listenerName, level, approver string }

func (e levelNotAuthorized) Error() string {
	return fmt.Sprintf("listener %q is not configured to hold the %s risk level for the %s approver",
		e.listenerName, e.level, e.approver)
}

// maxDecisionReasonBytes caps the approval agent's reason. It is stored and
// shown to the person the statement was denied to.
const maxDecisionReasonBytes = 4096

// The approval agent's decisions, as a sidecar files them.
const (
	decisionApproved = "approved"
	decisionRejected = "rejected"
)

// PostReview
//
//	@Summary		Create Sidecar Approval Request
//	@Description	Register an approval request for a statement a sidecar held. The sidecar is taken from the token, never the body. An approval request for the same bytes past its deadline is expired and a new one is filed. With risk_level, the listener's analyzer level decides who approves; with approver ai_agent the request records the approval agent's decision, is filed decided and never releases the statement.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			hoop-sidecar-token		header		string							false	"The token returned when the sidecar was created. Omit it when sending hoop-sidecar-identity."
//	@Param			hoop-sidecar-identity	header		string							false	"A Kubernetes or Google service account JWT, raw, that a sidecar service account mapping allows. Omit it when sending hoop-sidecar-token."
//	@Param			request					body		openapi.SidecarReviewRequest	true	"The request body resource"
//	@Success		200						{object}	openapi.SidecarReviewResponse
//	@Success		201						{object}	openapi.SidecarReviewResponse
//	@Failure		400,401,413,422,500		{object}	openapi.HTTPError
//	@Router			/sidecars/reviews [post]
func PostReview(c *gin.Context) {
	sidecar := authenticatedSidecar(c)
	if sidecar == nil {
		return
	}

	var req openapi.SidecarReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	// Told here, not by a failed write: no index holds a longer name.
	if n := len(req.ListenerName); n > models.MaxSidecarListenerNameBytes {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf(
			"listener_name is %d bytes, over %d", n, models.MaxSidecarListenerNameBytes)})
		return
	}
	if err := checkFilingFields(req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	// Decoded here so a human never sees base64. The raw bytes are capped and
	// hashed; the blobs and Slack get displayStatement(raw).
	statement, err := base64.StdEncoding.DecodeString(req.Payload)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("payload is not valid base64: %v", err)})
		return
	}

	if len(statement) > maxStatementBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"message": fmt.Sprintf("statement is larger than %d bytes", maxStatementBytes),
		})
		return
	}

	if req.RiskLevel != "" {
		postLevelReview(c, sidecar, req, statement)
		return
	}

	rule, err := authorizedApprovalRule(models.DB, sidecar, req.ListenerName, req.ApprovalRule)
	if err != nil {
		var refusal ruleNotAuthorized
		if errors.As(err, &refusal) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": refusal.Error()})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading the approval rule")
		return
	}

	// EVL-286 stores a sidecar rule without checking its reviewer settings, so
	// this is the first place they are checked.
	if err := approvableSidecarRule(rule); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}

	// A rule naming nobody produces a review with no group row, which no
	// approval can ever settle.
	policy, err := services.ReviewPolicyFromRule(sidecar.OrgID, rule)
	if err != nil {
		if errors.Is(err, services.ErrRuleHasNoReviewers) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed building the approval policy")
		return
	}
	fileHeldStatement(c, sidecar, req, statement, ruleFiling(rule, policy))
}

// ruleFiling files under an approval rule: its groups, minimum, force
// groups and limits.
func ruleFiling(rule *models.AccessRequestRule, policy *services.ReviewPolicy) sidecarFiling {
	return sidecarFiling{
		key:         models.SidecarReviewKey{RuleName: rule.Name},
		label:       fmt.Sprintf("rule %q", rule.Name),
		ruleName:    &rule.Name,
		policy:      policy,
		forceGroups: rule.ForceApprovalGroups,
		pendingTTL:  rule.PendingTTLSec,
		approvalTTL: rule.ApprovalTTLSec,
	}
}

// checkFilingFields refuses a body whose fields do not fit together: a rule
// or a risk level, never both, and a decision only from the approval agent.
func checkFilingFields(req openapi.SidecarReviewRequest) error {
	if req.RiskLevel == "" {
		switch {
		case req.ApprovalRule == "":
			return errors.New("approval_rule or risk_level is required")
		case req.Approver != "" || req.Decision != "" || req.Reason != "":
			return errors.New("approver, decision and reason are read only with risk_level")
		}
		return nil
	}
	if !analyzer.RiskLevel(req.RiskLevel).Valid() {
		return fmt.Errorf("risk_level %q is not high, medium or low", req.RiskLevel)
	}
	if req.ApprovalRule != "" {
		return errors.New("approval_rule and risk_level are exclusive")
	}
	switch analyzer.Approver(req.Approver) {
	case analyzer.ApproverHuman:
		if req.Decision != "" || req.Reason != "" {
			return errors.New("decision and reason are read only with approver ai_agent")
		}
	case analyzer.ApproverAIAgent:
		if req.Decision != decisionApproved && req.Decision != decisionRejected {
			return fmt.Errorf("decision %q is not approved or rejected", req.Decision)
		}
		if strings.TrimSpace(req.Reason) == "" {
			return errors.New("reason is required with a decision")
		}
		if len(req.Reason) > maxDecisionReasonBytes {
			return fmt.Errorf("reason is %d bytes, over %d", len(req.Reason), maxDecisionReasonBytes)
		}
		if req.Reserve {
			return errors.New("reserve has no meaning on a decided approval request")
		}
	default:
		return fmt.Errorf("approver %q is not human or ai_agent", req.Approver)
	}
	return nil
}

// postLevelReview files a statement held under a risk level. The level in the
// listener's composed analyzer block decides who approves; the body only
// names it.
func postLevelReview(c *gin.Context, sidecar *models.Sidecar, req openapi.SidecarReviewRequest, statement []byte) {
	level := analyzer.RiskLevel(req.RiskLevel)
	approver := analyzer.Approver(req.Approver)
	held, owner, err := authorizedRiskLevel(models.DB, sidecar, req.ListenerName, level, approver)
	if err != nil {
		var refusal levelNotAuthorized
		if errors.As(err, &refusal) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": refusal.Error()})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading the risk level")
		return
	}
	if approver == analyzer.ApproverAIAgent {
		fileAgentDecision(c, sidecar, req, statement)
		return
	}

	// The limits live on the managed approval rule of the analyzer rule bound
	// here; a listener's own block has none.
	filing := sidecarFiling{
		key:    models.SidecarReviewKey{RiskLevel: string(level)},
		label:  fmt.Sprintf("the %s risk level", level),
		policy: services.ReviewPolicyForLevel(sidecar.OrgID, held),
		level:  string(level),
	}
	if owner != "" {
		orgID, err := uuid.Parse(sidecar.OrgID)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed parsing the sidecar organization id")
			return
		}
		managed, err := services.AnalyzerApprovalRule(models.DB, orgID, owner)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading the approval limits")
			return
		}
		if managed != nil {
			for name, ttl := range map[string]*int{"pending_ttl_sec": managed.PendingTTLSec, "approval_ttl_sec": managed.ApprovalTTLSec} {
				if err := services.CheckSidecarReviewTTL(name, ttl); err != nil {
					c.JSON(http.StatusUnprocessableEntity, gin.H{"message": fmt.Sprintf("analyzer rule %q: %v", owner, err)})
					return
				}
			}
			filing.pendingTTL, filing.approvalTTL = managed.PendingTTLSec, managed.ApprovalTTLSec
		}
	}
	fileHeldStatement(c, sidecar, req, statement, filing)
}

// fileHeldStatement answers from the live review of these bytes, or files a
// new one under filing.
func fileHeldStatement(c *gin.Context, sidecar *models.Sidecar, req openapi.SidecarReviewRequest, statement []byte, filing sidecarFiling) {
	// The exact bytes bind the approval to one statement. The sidecar's analyzer
	// cache key strips literals, and a key of that shape would let an approval
	// of `DELETE ... WHERE id = 1` release `id = 999`.
	statementHash := models.HashStatement(statement)

	// What the reviewer reads. The match stays on statementHash.
	display := displayStatement(statement)

	// Two passes. A match can be consumed between the read and the insert, and
	// an insert can lose the index to a racing request whose review is then
	// consumed before the re-read. Either way one more pass settles it.
	// One clock for both, so the insert expires the holder the read found lapsed.
	now := time.Now().UTC()
	const attempts = 2
	for range attempts {
		rev, err := models.GetLiveSidecarReview(models.DB, sidecar.OrgID, sidecar.ID,
			req.ListenerName, filing.key, statementHash, now)
		switch {
		case err == nil:
			answerExistingReview(c, sidecar, req.ListenerName, rev, time.Now().UTC(), req.Reserve)
			return
		case !errors.Is(err, gorm.ErrRecordNotFound):
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading the sidecar approval request")
			return
		}

		sessionID := uuid.NewString()
		rev = newSidecarReview(sidecar, req.ListenerName, sessionID, statementHash, filing, now)
		expiredIDs, err := createSidecarReview(sidecar, req.ListenerName, display,
			newSidecarSession(sidecar, sessionID, display, now), rev)
		switch {
		case errors.Is(err, gorm.ErrDuplicatedKey):
			// A racing request filed first. Look again rather than answer: its
			// review is normally there to answer from, and if it was consumed
			// in between then nothing is live and this statement needs its own.
			continue
		case err != nil:
			// The error is logged and sent to Sentry by AbortWithErr; the caller
			// gets none of it. A database message names constraints, tables and
			// columns, and a token holder has no use for any of that.
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating sidecar approval request")
			return
		}

		if len(expiredIDs) > 0 {
			log.With("review-id", rev.ID, "expired", expiredIDs, "sidecar", sidecar.Name).
				Infof("expired the lapsed review of this statement")
			// Detached like notifySlack, and only when there is a Slack to rewrite.
			if slackservice.GetServiceInstance(sidecar.OrgID) != nil {
				go reviewapi.PublishSidecarExpiry(sidecar.OrgID, expiredIDs)
			}
		}

		answerFiledReview(c, sidecar, req, filing.label, rev, display)
		return
	}

	// Both passes lost the race. Refuse instead of looping: the statement is
	// being filed and consumed faster than a request can answer it, and a
	// non-2xx makes the sidecar deny.
	httputils.AbortWithErr(c, http.StatusInternalServerError,
		fmt.Errorf("could not match or file an approval request in %d attempts", attempts),
		"failed creating sidecar approval request")
}

// fileAgentDecision records what the sidecar's approval agent decided. The
// review is written decided and is final: it matches no later filing, and a
// claim never releases it. The sidecar already acted on the decision.
func fileAgentDecision(c *gin.Context, sidecar *models.Sidecar, req openapi.SidecarReviewRequest, statement []byte) {
	now := time.Now().UTC()
	display := displayStatement(statement)
	sessionID := uuid.NewString()
	reason := req.Reason

	rev := newSidecarReview(sidecar, req.ListenerName, sessionID, "", sidecarFiling{level: req.RiskLevel}, now)
	rev.ApproverType = models.ApproverTypeAIAgent
	rev.DecisionReason = &reason
	sess := newSidecarSession(sidecar, sessionID, display, now)
	// Closed at once: an approved statement runs now, a rejected one never.
	sess.Status = string(openapi.SessionStatusDone)
	if req.Decision == decisionApproved {
		rev.Status = models.ReviewStatusApproved
		sess.EndSession = &now
	} else {
		rev.Status = models.ReviewStatusRejected
		rev.RejectionReason = &reason
	}

	if _, err := createSidecarReview(sidecar, req.ListenerName, display, sess, rev); err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed recording the approval agent decision")
		return
	}
	log.With("sid", rev.SessionID, "review-id", rev.ID, "sidecar", sidecar.Name, "listener", req.ListenerName,
		"risk-level", req.RiskLevel, "decision", req.Decision).Infof("recorded an approval agent decision")

	trackClient := analytics.New()
	defer trackClient.Close()
	trackClient.TrackEvent(analytics.EventCreateSidecarReview, map[string]any{
		"org-id":   sidecar.OrgID,
		"sidecar":  sidecar.Name,
		"listener": req.ListenerName,
	})
	// The decision a person makes on the approvals page tracks this event. No
	// user made this one, so it goes out as a system event.
	trackClient.TrackEvent(analytics.EventSessionReviewed, map[string]any{
		"org-id":        sidecar.OrgID,
		"session-id":    rev.SessionID,
		"sidecar":       sidecar.Name,
		"listener":      req.ListenerName,
		"status":        string(rev.Status),
		"approver-type": models.ApproverTypeAIAgent,
		"risk-level":    req.RiskLevel,
	})

	// No Slack message: nobody acts on a decided review.
	c.JSON(http.StatusCreated, &openapi.SidecarReviewResponse{
		Forward: false,
		Review:  toOpenApiSidecarReview(rev),
	})
}

// ClaimReview
//
//	@Summary		Claim Sidecar Approval Request
//	@Description	Answer a sidecar waiting on one approval request it filed. An approved request is consumed once and releases the statement, or is reserved (PROCESSING) when the body asks to reserve it; a request past its deadline is expired and never releases it; any other status is returned as it stands. It never files an approval request.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			hoop-sidecar-token		header		string								false	"The token returned when the sidecar was created. Omit it when sending hoop-sidecar-identity."
//	@Param			hoop-sidecar-identity	header		string								false	"A Kubernetes or Google service account JWT, raw, that a sidecar service account mapping allows. Omit it when sending hoop-sidecar-token."
//	@Param			id						path		string								true	"The approval request id"
//	@Param			request					body		openapi.SidecarReviewClaimRequest	false	"Optional; an empty body spends an approved request"
//	@Success		200						{object}	openapi.SidecarReviewResponse
//	@Failure		400,401,404,500			{object}	openapi.HTTPError
//	@Router			/sidecars/reviews/{id}/claim [post]
func ClaimReview(c *gin.Context) {
	sidecar := authenticatedSidecar(c)
	if sidecar == nil {
		return
	}

	// Optional: a sidecar that predates settling sends no body.
	var req openapi.SidecarReviewClaimRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	// Parsed before the query: the column is a uuid, and Postgres answers a
	// malformed one with an error that would read as a 500.
	reviewID := c.Param("id")
	if _, err := uuid.Parse(reviewID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "approval request not found"})
		return
	}

	// Scoped to the calling sidecar, so a token cannot claim another
	// sidecar's approval by guessing its id.
	rev, err := models.GetSidecarReview(models.DB, sidecar.OrgID, sidecar.ID, reviewID)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "approval request not found"})
		return
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading the sidecar approval request")
		return
	}
	answerExistingReview(c, sidecar, rev.ListenerName.String, rev, time.Now().UTC(), req.Reserve)
}

// SettleReview
//
//	@Summary		Settle Sidecar Approval Request
//	@Description	Report what the upstream answered to a statement a reserved approval released. Accepted spends the approval (EXECUTED). Refused gives it back (APPROVED), so a resend of the same bytes can take it again. A reservation past its lease is spent and stays spent. Any approval request the calling sidecar did not reserve is returned as it stands.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			hoop-sidecar-token		header		string								false	"The token returned when the sidecar was created. Omit it when sending hoop-sidecar-identity."
//	@Param			hoop-sidecar-identity	header		string								false	"A Kubernetes or Google service account JWT, raw, that a sidecar service account mapping allows. Omit it when sending hoop-sidecar-token."
//	@Param			id						path		string								true	"The approval request id"
//	@Param			request					body		openapi.SidecarReviewSettleRequest	true	"The request body resource"
//	@Success		200						{object}	openapi.SidecarReviewResponse
//	@Failure		400,401,404,500			{object}	openapi.HTTPError
//	@Router			/sidecars/reviews/{id}/settle [post]
func SettleReview(c *gin.Context) {
	sidecar := authenticatedSidecar(c)
	if sidecar == nil {
		return
	}

	reviewID := c.Param("id")
	if _, err := uuid.Parse(reviewID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "approval request not found"})
		return
	}
	var req openapi.SidecarReviewSettleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	// Read first, scoped to the calling sidecar, so a token cannot settle
	// another sidecar's reservation or write anything on its review.
	switch _, err := models.GetSidecarReview(models.DB, sidecar.OrgID, sidecar.ID, reviewID); {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "approval request not found"})
		return
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading the sidecar approval request")
		return
	}
	settled, err := models.SettleSidecarReview(models.DB, sidecar.OrgID, sidecar.ID, reviewID, *req.Accepted, time.Now().UTC())
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed settling the sidecar approval request")
		return
	}
	rev, err := models.GetSidecarReview(models.DB, sidecar.OrgID, sidecar.ID, reviewID)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading the sidecar approval request")
		return
	}

	if settled {
		listenerName := rev.ListenerName.String
		log.With("sid", rev.SessionID, "review-id", rev.ID, "sidecar", sidecar.Name,
			"listener", listenerName, "accepted", *req.Accepted).
			Infof("settled a reserved sidecar review")
		if *req.Accepted {
			trackConsumedReview(sidecar, listenerName)
		}
	}
	c.JSON(http.StatusOK, &openapi.SidecarReviewResponse{
		Forward: false,
		Review:  toOpenApiSidecarReview(rev),
	})
}

// GetReview
//
//	@Summary		Get Sidecar Approval Status
//	@Description	Read the status of one approval request the calling sidecar filed. It never changes the request: an approved request stays approved until the sidecar resends the statement or its approval expires. A request past its deadline reads EXPIRED.
//	@Tags			Sidecars
//	@Produce		json
//	@Param			hoop-sidecar-token		header		string	false	"The token returned when the sidecar was created. Omit it when sending hoop-sidecar-identity."
//	@Param			hoop-sidecar-identity	header		string	false	"A Kubernetes or Google service account JWT, raw, that a sidecar service account mapping allows. Omit it when sending hoop-sidecar-token."
//	@Param			id						path		string	true	"The approval request id"
//	@Success		200						{object}	openapi.SidecarReviewStatus
//	@Failure		401,404,500				{object}	openapi.HTTPError
//	@Router			/sidecars/reviews/{id} [get]
func GetReview(c *gin.Context) {
	sidecar := authenticatedSidecar(c)
	if sidecar == nil {
		return
	}

	reviewID := c.Param("id")
	if _, err := uuid.Parse(reviewID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "approval request not found"})
		return
	}

	// Another sidecar's review reads as not found, so a token cannot probe ids.
	rev, err := models.GetSidecarReview(models.DB, sidecar.OrgID, sidecar.ID, reviewID)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "approval request not found"})
		return
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading the sidecar approval request")
		return
	}
	c.JSON(http.StatusOK, toSidecarReviewStatus(rev))
}

// Bounds of ListReviews' limit. A sidecar files a review per held statement,
// so the list grows without end; an agent reads the newest page.
const (
	defaultReviewListLimit = 50
	maxReviewListLimit     = 200
)

// ListReviews
//
//	@Summary		List Sidecar Approval Requests
//	@Description	List the approval requests the calling sidecar filed, newest first. It never changes a request.
//	@Tags			Sidecars
//	@Produce		json
//	@Param			hoop-sidecar-token		header		string	false	"The token returned when the sidecar was created. Omit it when sending hoop-sidecar-identity."
//	@Param			hoop-sidecar-identity	header		string	false	"A Kubernetes or Google service account JWT, raw, that a sidecar service account mapping allows. Omit it when sending hoop-sidecar-token."
//	@Param			status					query		string	false	"Only approval requests in this status"				Enums(PENDING, APPROVED, REJECTED, REVOKED, PROCESSING, EXECUTED, EXPIRED, UNKNOWN)
//	@Param			limit					query		int		false	"The most approval requests to return, 1 to 200"	default(50)
//	@Success		200						{array}		openapi.SidecarReviewStatus
//	@Failure		400,401,500				{object}	openapi.HTTPError
//	@Router			/sidecars/reviews [get]
func ListReviews(c *gin.Context) {
	sidecar := authenticatedSidecar(c)
	if sidecar == nil {
		return
	}

	status := models.ReviewStatusType(strings.ToUpper(c.Query("status")))
	switch status {
	case "", models.ReviewStatusPending, models.ReviewStatusApproved, models.ReviewStatusRejected,
		models.ReviewStatusRevoked, models.ReviewStatusProcessing, models.ReviewStatusExecuted,
		models.ReviewStatusExpired, models.ReviewStatusUnknown:
	default:
		c.JSON(http.StatusBadRequest, gin.H{"message": fmt.Sprintf("unknown approval status %q", c.Query("status"))})
		return
	}
	limit := defaultReviewListLimit
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxReviewListLimit {
			c.JSON(http.StatusBadRequest, gin.H{
				"message": fmt.Sprintf("limit must be a number from 1 to %d", maxReviewListLimit)})
			return
		}
		limit = n
	}

	reviews, err := models.ListSidecarReviews(models.DB, sidecar.OrgID, sidecar.ID, status, limit)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed listing the sidecar approval requests")
		return
	}
	out := make([]*openapi.SidecarReviewStatus, 0, len(reviews))
	for i := range reviews {
		out = append(out, toSidecarReviewStatus(&reviews[i]))
	}
	c.JSON(http.StatusOK, out)
}

// authenticatedSidecar returns the sidecar the token named, or answers the
// request and returns nil.
func authenticatedSidecar(c *gin.Context) *models.Sidecar {
	sidecar := apiroutes.SidecarFromContext(c)
	if sidecar == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return nil
	}
	return sidecar
}

// answerFiledReview reports a review this request filed. Forward is false: it
// was filed a moment ago and no human has seen it.
func answerFiledReview(c *gin.Context, sidecar *models.Sidecar, req openapi.SidecarReviewRequest,
	label string, rev *models.Review, display string) {
	log.With("sid", rev.SessionID, "review-id", rev.ID, "sidecar", sidecar.Name,
		"listener", req.ListenerName, "under", label).
		Infof("registered a sidecar review")

	// TrackEvent, not TrackRequest: this request has no user, and TrackRequest
	// answers an empty user email by doing nothing at all.
	trackClient := analytics.New()
	defer trackClient.Close()
	trackClient.TrackEvent(analytics.EventCreateSidecarReview, map[string]any{
		"org-id":   sidecar.OrgID,
		"sidecar":  sidecar.Name,
		"listener": req.ListenerName,
	})

	// Detached, and deliberately after the review exists. SendMessageReview
	// sleeps 1200ms per channel and the Slack client carries no timeout, so on
	// the request path this would hold the sidecar for at least that long and
	// potentially forever, while it waits on a statement it is blocking. A
	// sidecar that gives up and retries would then file the review twice.
	//
	// Nothing in the response depends on it: the review is already persisted
	// and visible to an approver either way.
	go notifySlack(sidecar, rev, req.ListenerName, display)

	// Forward is false: the review was filed a moment ago and no human has
	// seen it. The sidecar denies this statement and carries the review id.
	c.JSON(http.StatusCreated, &openapi.SidecarReviewResponse{
		Forward: false,
		Review:  toOpenApiSidecarReview(rev),
	})
}

// answerExistingReview answers a retry, or a claim by id, from the filed review. Only the
// claim's winner of an APPROVED review forwards; a lapsed one is expired; others return as they stand.
// reserve takes the approval without spending it; see ReserveApprovedSidecarReview.
func answerExistingReview(c *gin.Context, sidecar *models.Sidecar, listenerName string, rev *models.Review, now time.Time, reserve bool) {
	// The approval agent's decision is final, and the sidecar acted on it
	// when it filed it: nothing here releases it again.
	if rev.DecidedByAgent() {
		c.JSON(http.StatusOK, &openapi.SidecarReviewResponse{Forward: false, Review: toOpenApiSidecarReview(rev)})
		return
	}
	forward := false
	if rev.Status == models.ReviewStatusApproved {
		var ok bool
		if forward, ok = claimSidecarApproval(c, sidecar, listenerName, rev, now, reserve); !ok {
			return
		}
	}

	// A lost claim leaves the row APPROVED only when the deadline refused it, by
	// the database clock, which can pass it while the request waits on the lock.
	refusedByDeadline := rev.Status == models.ReviewStatusApproved
	if !forward && (refusedByDeadline || lapsedSidecarReview(rev, now)) {
		expired, status, err := models.ExpireSidecarReview(models.DB, sidecar.OrgID, rev.ID, now)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed expiring the sidecar approval request")
			return
		}
		rev.Status = status
		if expired {
			log.With("sid", rev.SessionID, "review-id", rev.ID, "sidecar", sidecar.Name,
				"listener", listenerName).Infof("expired a sidecar review")
			if slackservice.GetServiceInstance(sidecar.OrgID) != nil {
				go reviewapi.PublishSidecarExpiry(sidecar.OrgID, []string{rev.ID})
			}
		}
		// An approval committed after the read and moved the deadline. The
		// sidecar reads APPROVED with forward false as final, so claim it now.
		if status == models.ReviewStatusApproved {
			var ok bool
			if forward, ok = claimSidecarApproval(c, sidecar, listenerName, rev, time.Now().UTC(), reserve); !ok {
				return
			}
		}
	}

	c.JSON(http.StatusOK, &openapi.SidecarReviewResponse{
		Forward: forward,
		Review:  toOpenApiSidecarReview(rev),
	})
}

// lapsedSidecarReview: the review read as live, or as EXPIRED before the row
// records it, and its deadline passed at now.
func lapsedSidecarReview(rev *models.Review, now time.Time) bool {
	switch rev.Status {
	case models.ReviewStatusPending, models.ReviewStatusApproved, models.ReviewStatusExpired:
		return rev.ExpiresAt != nil && !now.Before(*rev.ExpiresAt)
	}
	return false
}

// claimSidecarApproval consumes an approved review, or reserves it, and
// reports whether this request won it. ok is false when it already answered
// the request.
func claimSidecarApproval(c *gin.Context, sidecar *models.Sidecar, listenerName string, rev *models.Review,
	now time.Time, reserve bool) (forward, ok bool) {
	take := models.ClaimApprovedSidecarReview
	if reserve {
		take = models.ReserveApprovedSidecarReview
	}
	claimed, status, err := take(models.DB, sidecar.OrgID, rev.ID, now)
	if err != nil {
		httputils.AbortWithErr(c, http.StatusInternalServerError, err,
			"failed consuming the approved sidecar approval request")
		return false, false
	}
	// The status the row holds now, so a claim loser reports EXECUTED (or
	// PROCESSING) rather than the APPROVED it read a moment earlier. It is
	// forward, not this, that says whether the statement may run.
	rev.Status = status
	if !claimed {
		return false, true
	}

	if reserve {
		// Not spent yet: SettleReview counts it once the upstream accepts.
		log.With("sid", rev.SessionID, "review-id", rev.ID, "sidecar", sidecar.Name,
			"listener", listenerName).
			Infof("reserved an approved sidecar review")
		return true, true
	}
	log.With("sid", rev.SessionID, "review-id", rev.ID, "sidecar", sidecar.Name,
		"listener", listenerName).
		Infof("consumed an approved sidecar review")
	trackConsumedReview(sidecar, listenerName)
	return true, true
}

// trackConsumedReview records that an approval was spent on a statement.
func trackConsumedReview(sidecar *models.Sidecar, listenerName string) {
	trackClient := analytics.New()
	defer trackClient.Close()
	trackClient.TrackEvent(analytics.EventConsumeSidecarReview, map[string]any{
		"org-id":   sidecar.OrgID,
		"sidecar":  sidecar.Name,
		"listener": listenerName,
	})
}

// notifySlack posts the review to the org's Slack channel, so a human learns it
// exists rather than finding it by looking.
//
// Slack is optional everywhere else in this codebase and stays optional here:
// an org that has not configured it gets no message and no error.
func notifySlack(sidecar *models.Sidecar, rev *models.Review, listenerName, display string) {
	slackSvc := slackservice.GetServiceInstance(sidecar.OrgID)
	if slackSvc == nil {
		return
	}

	// The listener's channels, else the org default channel as a fallback.
	// With neither, say so: otherwise a misconfigured org gets silence that
	// looks like success. A failed read still leaves the default channel.
	channels, err := models.ResolveSidecarSlackChannels(models.DB, sidecar.OrgID, sidecar.ID, listenerName)
	if err != nil {
		log.With("sid", rev.SessionID, "review-id", rev.ID).
			Warnf("failed reading the listener slack channels, posting to the default channel only, reason=%v", err)
		channels = nil
	}
	if len(channels) == 0 && slackSvc.DefaultChannel() == "" {
		log.With("sid", rev.SessionID, "review-id", rev.ID).
			Warnf("no slack channel is set for this listener or the org, nobody was notified of this review")
		return
	}

	req := newSlackReviewRequest(sidecar, rev, listenerName, display)
	req.SlackChannels = channels
	req.DefaultChannelAsFallback = true
	// The same ceiling both existing senders apply. Two groups today, but a
	// message with no buttons is a notification nobody can act on.
	if len(req.ApprovalGroups) == 0 || len(req.ApprovalGroups) >= slackplugin.SlackMaxButtons {
		log.With("review-id", rev.ID).Warnf("not sending a review message with %d approval groups",
			len(req.ApprovalGroups))
		return
	}

	result := slackSvc.PostMessageReview(req)
	if result.Posted == 0 {
		// Every channel refused it: a wrong id, or a private channel the app
		// was not invited to. Nobody can act on this review from Slack.
		log.With("sid", rev.SessionID, "review-id", rev.ID).
			Warnf("no slack channel accepted the review message, nobody was notified, %v", result)
		return
	}
	log.With("sid", rev.SessionID, "review-id", rev.ID).Infof("slack review message, %v", result)
}

// newSlackReviewRequest is what a reviewer ends up reading. Split out so the
// message can be asserted without a Slack workspace.
func newSlackReviewRequest(sidecar *models.Sidecar, rev *models.Review, listenerName, display string) *slackservice.MessageReviewRequest {
	// The resource the admin sees when the listener has a mirror, else the
	// listener itself.
	connection := rev.ConnectionName
	if connection == "" {
		connection = listenerName
	}
	return &slackservice.MessageReviewRequest{
		// ID is load bearing: it becomes the message metadata and the button
		// ids, and it is how a click finds its way back to this review.
		ID:        rev.ID,
		SessionID: rev.SessionID,

		// The message renders these labels whether or not they mean anything
		// here, so each carries what a sidecar review does know rather than a
		// blank that reads as broken. Groups repeats the eligible roles the
		// buttons are labelled with: a sidecar has no groups of its own, and
		// saying who may act beats an empty field.
		Name:           sidecar.Name,
		UserGroups:     slackplugin.ParseGroups(rev.ReviewGroups),
		Email:          reviewOwnerEmail,
		Connection:     connection,
		ConnectionType: reviewSlackType,

		ApprovalGroups: slackplugin.ParseGroups(rev.ReviewGroups),
		Script:         display,

		// FullApiURL, not ApiURL: the latter drops the configured path prefix,
		// which lands the approver outside the app wherever one is set.
		WebappURL: fmt.Sprintf("%s/approvals/%s", appconfig.Get().FullApiURL(), rev.SessionID),

		// The decision deadline; nil with no limit.
		ExpiresAt: rev.ExpiresAt,
	}
}

// approvableSidecarRule refuses a rule that cannot produce a review a human is
// able to settle, or whose stored policy is not the policy that would be
// enforced.
//
// It lives on this path rather than inside services.ReviewPolicyFromRule
// because the gateway's AI review path shares that function. A rule refused
// here is one a gateway files reviews against today, and this project changes
// the control plane only.
//
// The control plane stores a sidecar rule after checking its name, its access
// type and that it targets no connection (EVL-286). Nothing checks the fields
// below, so they are checked here instead of trusted.
func approvableSidecarRule(rule *models.AccessRequestRule) error {
	seen := make(map[string]struct{}, len(rule.ReviewersGroups))
	for _, groupName := range rule.ReviewersGroups {
		// A blank name matches no group, so its row stays pending forever and
		// takes the review with it.
		if strings.TrimSpace(groupName) == "" {
			return fmt.Errorf("access request rule %q has a blank entry in reviewers_groups", rule.Name)
		}
		// One approval marks every row whose group the approver is in, so a
		// repeated group lets one person satisfy several approvals.
		if _, duplicate := seen[groupName]; duplicate {
			return fmt.Errorf("access request rule %q repeats the reviewers_groups entry %q",
				rule.Name, groupName)
		}
		seen[groupName] = struct{}{}
	}

	// doIndividualReview reads the minimum as min(group rows, minimum) and
	// ignores it entirely below 1. Either bound would persist and return a
	// number that is not the number enforced, so refuse rather than record a
	// policy the review does not follow.
	if !rule.AllGroupsMustApprove && rule.MinApprovals != nil {
		switch min := *rule.MinApprovals; {
		case min < 1:
			return fmt.Errorf("access request rule %q sets min_approvals to %d, below 1", rule.Name, min)
		case min > len(rule.ReviewersGroups):
			return fmt.Errorf("access request rule %q sets min_approvals to %d, more than its %d reviewers_groups",
				rule.Name, min, len(rule.ReviewersGroups))
		}
	}

	// The rule write paths check these too, but a stored value is not trusted.
	if err := services.CheckSidecarReviewTTL("pending_ttl_sec", rule.PendingTTLSec); err != nil {
		return fmt.Errorf("access request rule %q: %w", rule.Name, err)
	}
	if err := services.CheckSidecarReviewTTL("approval_ttl_sec", rule.ApprovalTTLSec); err != nil {
		return fmt.Errorf("access request rule %q: %w", rule.Name, err)
	}
	return nil
}

// authorizedApprovalRule loads the rule the request names and checks that the
// calling sidecar may file against it.
//
// Authorization comes from the sidecar's STORED configuration, never from the
// body: the request could otherwise name any rule in the organization and pick
// its own approvers. The listener must exist in that configuration and its
// analyzer block must name this rule (EVL-294).
func authorizedApprovalRule(db *gorm.DB, sidecar *models.Sidecar, listenerName, ruleName string) (*models.AccessRequestRule, error) {
	refuse := ruleNotAuthorized{listenerName: listenerName, ruleName: ruleName}

	// Against the document SERVED to this build: composed, and downgraded
	// for a build without the object form (the capabilities its last
	// handshake reported). A listener's analyzer block can come from a rule
	// an admin bound in the control plane, and composition places it on the
	// way out without storing it; an old build is served a bare hold under
	// the managed approval rule that the stored spec no longer names.
	// Authorizing against anything else would refuse every review the
	// sidecar files, and the statement would be denied forever.
	//
	// Composition failing is not an authorization answer: it means the rules
	// could not be read or do not fit together, so it is reported rather than
	// rendered as a refusal the admin would look for in their reviewer list.
	// A build the plane refuses to serve is refused here too.
	listeners := sidecar.Configuration.Listeners
	if db != nil {
		served, err := services.ServedSidecarConfiguration(db, sidecar)
		var missing services.ErrSidecarCapabilityMissing
		switch {
		case errors.As(err, &missing):
			return nil, refuse
		case err != nil:
			return nil, fmt.Errorf("failed composing the sidecar configuration: %w", err)
		}
		listeners = served.Listeners
	}
	if !listenerNamesApprovalRule(listeners, listenerName, ruleName) {
		return nil, refuse
	}

	orgID, err := uuid.Parse(sidecar.OrgID)
	if err != nil {
		return nil, fmt.Errorf("failed parsing the sidecar organization id: %w", err)
	}

	rule, err := models.GetAccessRequestRuleByName(db, ruleName, orgID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, refuse
		}
		return nil, err
	}
	// A control plane stores only sidecar rules today, so this guard fires for
	// nothing that exists yet. It is here so a rule of another kind arriving
	// later cannot be approved through a listener: the access type is what
	// makes a rule a sidecar rule, and nothing downstream reads it again.
	if rule.AccessType != models.AccessTypeSidecar {
		return nil, refuse
	}
	return rule, nil
}

// authorizedRiskLevel reads the level a filing names in the analyzer block
// SERVED to this listener, the composed one, as authorizedApprovalRule does.
// The level must hold for the approver the body names. It returns the level
// and the analyzer rule bound to the listener ("" for the listener's own
// block).
func authorizedRiskLevel(db *gorm.DB, sidecar *models.Sidecar, listenerName string, level analyzer.RiskLevel,
	approver analyzer.Approver) (daemon.RiskLevel, string, error) {
	refuse := levelNotAuthorized{listenerName: listenerName, level: string(level), approver: string(approver)}
	listeners := sidecar.Configuration.Listeners
	var owners map[string]string
	if db != nil {
		composed, err := services.ComposeSidecarConfigurationWithOwners(db, sidecar)
		if err != nil {
			return daemon.RiskLevel{}, "", fmt.Errorf("failed composing the sidecar configuration: %w", err)
		}
		listeners, owners = composed.Config.Listeners, composed.Analyzers
	}
	held, ok := listenerHoldsLevel(listeners, listenerName, level, approver)
	if !ok {
		return daemon.RiskLevel{}, "", refuse
	}
	return held, owners[listenerName], nil
}

// listenerHoldsLevel reports whether the served configuration holds this
// level of this listener for this approver, and returns the level. Free of
// the database, like listenerNamesApprovalRule.
func listenerHoldsLevel(listeners []daemon.ListenerConfig, listenerName string, level analyzer.RiskLevel,
	approver analyzer.Approver) (daemon.RiskLevel, bool) {
	block := uniqueListenerAnalyzer(listeners, listenerName)
	if block == nil {
		return daemon.RiskLevel{}, false
	}
	// A copy, folded: a block the plane did not decode through decodeSpec
	// can still carry an object hold with its ai_agent default unwritten.
	canonical := *block
	if err := canonical.Canonicalize(); err != nil {
		return daemon.RiskLevel{}, false
	}
	held, ok := services.AnalyzerBlockLevel(&canonical, level)
	if !ok || services.LevelApprover(held) != approver {
		return daemon.RiskLevel{}, false
	}
	return held, true
}

// listenerNamesApprovalRule reports whether the configuration SERVED to this
// sidecar gives this listener this approval rule. It takes the listeners
// rather than the sidecar, and is free of the database, so the whole
// authorization table can be asserted without Postgres -- and so the caller
// cannot forget that the document to read is the served one.
func listenerNamesApprovalRule(listeners []daemon.ListenerConfig, listenerName, ruleName string) bool {
	if ruleName == "" {
		return false
	}
	// A listener with no analyzer block, or one naming a different rule,
	// authorizes nothing. There is no inherited default: the people who may
	// release a statement against one database are not the people who may
	// release one against another.
	block := uniqueListenerAnalyzer(listeners, listenerName)
	if block == nil || block.ApprovalRule != ruleName {
		return false
	}
	// The rule releases only a level held in the bare form. Object levels
	// name their own approvers, and the sidecar ignores approval_rule beside
	// them; a filing under it would let the rule's groups release a statement
	// the level gives to other people or to the approval agent.
	canonical := *block
	if err := canonical.Canonicalize(); err != nil {
		return false
	}
	return services.AnalyzerBareHolds(&canonical)
}

// uniqueListenerAnalyzer returns the analyzer block of the one listener with
// this name, or nil.
func uniqueListenerAnalyzer(listeners []daemon.ListenerConfig, listenerName string) *daemon.LaneAnalyzerConfig {
	if listenerName == "" {
		return nil
	}
	var block *daemon.LaneAnalyzerConfig
	matches := 0
	for _, listener := range listeners {
		if listener.Name != listenerName {
			continue
		}
		matches++
		block = listener.Analyzer
	}

	// Listener names are NOT unique: daemon validation keys uniqueness on the
	// listen address. Two lanes may share a name and name different rules, and
	// the request says which listener but not which lane, so authorizing
	// against either one would let the sidecar choose the looser of the two.
	if matches != 1 {
		return nil
	}
	return block
}

// sidecarFiling is the policy one filing creates a review under: an approval
// rule, or a risk level of the listener's analyzer block.
type sidecarFiling struct {
	key   models.SidecarReviewKey
	label string
	// ruleName is nil under a risk level.
	ruleName *string
	// level is the risk level, "" under a rule.
	level string
	// policy is nil on a review the approval agent decided.
	policy                  *services.ReviewPolicy
	forceGroups             pq.StringArray
	pendingTTL, approvalTTL *int
}

// createSidecarReview writes the session and the review one statement needs to
// wait for a human. It returns gorm.ErrDuplicatedKey when a racing request
// filed for the same bytes first, and the ids of the lapsed reviews it expired.
//
// Both rows point at the listener's mirror connection when the org has one,
// so the review and the session show the resource the admin sees. A listener
// without a mirror (a row that fell behind the reconcile) files them with the
// listener alone.
//
// A review is one per session (private.reviews is UNIQUE on org and session),
// and UpdateReview syncs the session's status when the review settles, so the
// session is not optional bookkeeping.
func createSidecarReview(sidecar *models.Sidecar, listenerName, display string, sess models.Session, rev *models.Review) ([]string, error) {
	mirror, err := models.GetSidecarMirror(models.DB, sidecar.OrgID, sidecar.ID, listenerName)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("failed reading the listener's mirror connection: %w", err)
	}
	if mirror != nil {
		sess.Connection = mirror.Name
		rev.ConnectionName = mirror.Name
		rev.ConnectionID = sql.NullString{String: mirror.ID, Valid: true}
	}
	return models.CreateSidecarReview(models.DB, sess, rev, display)
}

// newSidecarSession is the session a sidecar review waits in. Its user is the sidecar.
func newSidecarSession(sidecar *models.Sidecar, sessionID, display string, now time.Time) models.Session {
	return models.Session{
		ID:             sessionID,
		OrgID:          sidecar.OrgID,
		BlobInput:      models.BlobInputType(display),
		ConnectionType: reviewConnectionType,
		Verb:           pb.ClientVerbExec,
		Status:         string(openapi.SessionStatusOpen),
		UserID:         sidecar.ID,
		UserName:       sidecar.Name,
		UserEmail:      reviewOwnerEmail,
		CreatedAt:      now,
	}
}

// newSidecarReview builds the row, and with it the whole approval policy the
// filing describes. Nothing in the review path spells that out, so an
// omission here is silent: without MinApprovals the review needs every group,
// and without a group row a role cannot approve at all.
func newSidecarReview(sidecar *models.Sidecar, listenerName, sessionID, statementHash string, filing sidecarFiling, now time.Time) *models.Review {
	rev := &models.Review{
		ID:           uuid.NewString(),
		OrgID:        sidecar.OrgID,
		Type:         models.ReviewTypeOneTime,
		Status:       models.ReviewStatusPending,
		SessionID:    sessionID,
		ApproverType: models.ApproverTypeHuman,

		// The listener this statement arrived on. It is what the review is
		// about and what decides the sidecar path; the connection, when set,
		// is the listener's mirror and never a listener name standing in for
		// one (connection_name is NOT NULL, and a bare listener name could
		// collide with a real connection of the same name).
		SidecarID:    sql.NullString{String: sidecar.ID, Valid: true},
		ListenerName: sql.NullString{String: listenerName, Valid: listenerName != ""},

		// What a retry of this statement matches on, and the reason the same
		// bytes are never reviewed twice while this review is live.
		StatementHash: sql.NullString{String: statementHash, Valid: statementHash != ""},

		OwnerID:    sidecar.ID,
		OwnerEmail: reviewOwnerEmail,
		OwnerName:  ptr.String(sidecar.Name),

		ForceApprovalGroups:   filing.forceGroups,
		AccessRequestRuleName: filing.ruleName,
		RiskLevel:             sql.NullString{String: filing.level, Valid: filing.level != ""},

		// Fixed at filing like the policy, so a rule edit moves no live review.
		ExpiresAt:      models.SidecarReviewDeadline(now, filing.pendingTTL),
		ApprovalTTLSec: copyInt(filing.approvalTTL),

		CreatedAt: now,
	}
	if filing.policy != nil {
		minApprovals := filing.policy.MinApprovals
		rev.MinApprovals = &minApprovals
		rev.ReviewGroups = filing.policy.Groups
	}
	return rev
}

func copyInt(v *int) *int {
	if v == nil {
		return nil
	}
	n := *v
	return &n
}

// toOpenApiSidecarReview renders what the sidecar needs to recognise the review
// later. The connection fields are left out: the sidecar acts on the listener
// and the statement, and the mirror, when there is one, is for the admin.
func toOpenApiSidecarReview(r *models.Review) *openapi.Review {
	groups := make([]openapi.ReviewGroup, 0, len(r.ReviewGroups))
	for _, rg := range r.ReviewGroups {
		groups = append(groups, openapi.ReviewGroup{
			ID:     rg.ID,
			Group:  rg.GroupName,
			Status: openapi.ReviewRequestStatusType(rg.Status),
		})
	}
	return &openapi.Review{
		ID:               r.ID,
		Session:          r.SessionID,
		Type:             openapi.ReviewType(r.Type),
		Status:           openapi.ReviewStatusType(r.Status),
		CreatedAt:        r.CreatedAt,
		ReviewGroupsData: groups,
		MinApprovals:     r.MinApprovals,
		SidecarID:        ptr.String(r.SidecarID.String),
		ListenerName:     ptr.String(r.ListenerName.String),

		// The policy the review was filed under, so a sidecar can record which
		// rule held a statement rather than only which rule it asked for.
		AccessRequestRuleName: r.AccessRequestRuleName,
		ForceApprovalGroups:   r.ForceApprovalGroups,
		RiskLevel:             nullStringPtr(r.RiskLevel),

		ExpiresAt:      r.SidecarExpiresAt(),
		ApprovalTTLSec: r.SidecarApprovalTTLSec(),

		// A held client reads it in the denial. A sidecar that predates it
		// ignores the field.
		RejectionReason: r.RejectionReason,
		ApproverType:    approverType(r),
		DecisionReason:  r.DecisionReason,
	}
}

// approverType is human for a row read before the column was selected.
func approverType(r *models.Review) string {
	if r.ApproverType == "" {
		return models.ApproverTypeHuman
	}
	return r.ApproverType
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func toSidecarReviewStatus(r *models.Review) *openapi.SidecarReviewStatus {
	return &openapi.SidecarReviewStatus{
		ID:              r.ID,
		Status:          openapi.ReviewStatusType(r.Status),
		ListenerName:    r.ListenerName.String,
		ApprovalRule:    ptr.ToString(r.AccessRequestRuleName),
		RiskLevel:       r.RiskLevel.String,
		ApproverType:    approverType(r),
		DecisionReason:  r.DecisionReason,
		CreatedAt:       r.CreatedAt,
		DecidedAt:       reviewDecidedAt(r),
		RejectionReason: r.RejectionReason,
		ExpiresAt:       r.SidecarExpiresAt(),
	}
}

// reviewDecidedAt is the latest group decision. private.reviews stores no
// decision time, so this can trail the status flip when a group decides after
// the minimum was met. The approval agent decides as it files.
func reviewDecidedAt(r *models.Review) *time.Time {
	if r.Status == models.ReviewStatusPending {
		return nil
	}
	if r.DecidedByAgent() {
		decided := r.CreatedAt
		return &decided
	}
	var decided *time.Time
	for _, rg := range r.ReviewGroups {
		if rg.Status == models.ReviewStatusPending || rg.ReviewedAt == nil {
			continue
		}
		if decided == nil || rg.ReviewedAt.After(*decided) {
			decided = rg.ReviewedAt
		}
	}
	return decided
}
