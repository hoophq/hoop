package apisidecar

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
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
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	slackservice "github.com/hoophq/hoop/gateway/slack"
	slackplugin "github.com/hoophq/hoop/gateway/transport/plugins/slack"
	"github.com/hoophq/hoop/sidecar/daemon"
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

// PostReview
//
//	@Summary		Create Sidecar Review
//	@Description	Register a review for a statement a sidecar held. The sidecar is taken from the token, never the body.
//	@Tags			Sidecars
//	@Accept			json
//	@Produce		json
//	@Param			hoop-sidecar-token	header		string							true	"The token returned when the sidecar was created"
//	@Param			request				body		openapi.SidecarReviewRequest	true	"The request body resource"
//	@Success		200						{object}	openapi.SidecarReviewResponse
//	@Success		201						{object}	openapi.SidecarReviewResponse
//	@Failure		400,401,412,413,422,500	{object}	openapi.HTTPError
//	@Router			/sidecars/reviews [post]
func PostReview(c *gin.Context) {
	sidecar := controlPlaneSidecar(c)
	if sidecar == nil {
		return
	}

	var req openapi.SidecarReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	// Decoded here so a human never sees base64. The statement is stored as
	// text and read back verbatim into the Slack message a reviewer approves
	// from, and nothing further down this path decodes anything.
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
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed building the review policy")
		return
	}

	// The exact bytes bind the approval to one statement. The sidecar's analyzer
	// cache key strips literals, and a key of that shape would let an approval
	// of `DELETE ... WHERE id = 1` release `id = 999`.
	statementHash := models.HashStatement(statement)

	// Two passes. A match can be consumed between the read and the insert, and
	// an insert can lose the index to a racing request whose review is then
	// consumed before the re-read. Either way one more pass settles it.
	const attempts = 2
	for range attempts {
		rev, err := models.GetLiveSidecarReview(models.DB, sidecar.OrgID, sidecar.ID,
			req.ListenerName, rule.Name, statementHash)
		switch {
		case err == nil:
			answerExistingReview(c, sidecar, req.ListenerName, rev)
			return
		case !errors.Is(err, gorm.ErrRecordNotFound):
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading the sidecar review")
			return
		}

		rev, err = createSidecarReview(sidecar, req.ListenerName, string(statement), statementHash, rule, policy)
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
			httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating sidecar review")
			return
		}

		answerFiledReview(c, sidecar, req, rule, rev, statement)
		return
	}

	// Both passes lost the race. Refuse instead of looping: the statement is
	// being filed and consumed faster than a request can answer it, and a
	// non-2xx makes the sidecar deny.
	httputils.AbortWithErr(c, http.StatusInternalServerError,
		fmt.Errorf("could not match or file a review in %d attempts", attempts),
		"failed creating sidecar review")
}

// ClaimReview
//
//	@Summary		Claim Sidecar Review
//	@Description	Answer a sidecar waiting on one review it filed. An approved review is consumed once and releases the statement; any other status is returned as it stands. It never files a review.
//	@Tags			Sidecars
//	@Produce		json
//	@Param			hoop-sidecar-token	header		string	true	"The token returned when the sidecar was created"
//	@Param			id					path		string	true	"The review id"
//	@Success		200					{object}	openapi.SidecarReviewResponse
//	@Failure		401,404,412,500		{object}	openapi.HTTPError
//	@Router			/sidecars/reviews/{id}/claim [post]
func ClaimReview(c *gin.Context) {
	sidecar := controlPlaneSidecar(c)
	if sidecar == nil {
		return
	}

	// Parsed before the query: the column is a uuid, and Postgres answers a
	// malformed one with an error that would read as a 500.
	reviewID := c.Param("id")
	if _, err := uuid.Parse(reviewID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "review not found"})
		return
	}

	// Scoped to the calling sidecar, so a token cannot claim another
	// sidecar's approval by guessing its id.
	rev, err := models.GetSidecarReview(models.DB, sidecar.OrgID, sidecar.ID, reviewID)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"message": "review not found"})
		return
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading the sidecar review")
		return
	}
	answerExistingReview(c, sidecar, rev.ListenerName.String, rev)
}

// controlPlaneSidecar returns the sidecar the token named, or answers the
// request and returns nil.
//
// A gateway would take a review and then never be able to settle it: approval
// there resolves a connection, and a sidecar review has none. The mode is
// checked after authentication, so an unauthenticated caller learns nothing
// about the deployment.
func controlPlaneSidecar(c *gin.Context) *models.Sidecar {
	sidecar := apiroutes.SidecarFromContext(c)
	if sidecar == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return nil
	}
	if !appconfig.Get().IsControlPlane() {
		c.JSON(http.StatusPreconditionFailed, gin.H{
			"message": "sidecar reviews are served by the control plane",
		})
		return nil
	}
	return sidecar
}

// answerFiledReview reports a review this request filed. Forward is false: it
// was filed a moment ago and no human has seen it.
func answerFiledReview(c *gin.Context, sidecar *models.Sidecar, req openapi.SidecarReviewRequest,
	rule *models.AccessRequestRule, rev *models.Review, statement []byte) {
	log.With("sid", rev.SessionID, "review-id", rev.ID, "sidecar", sidecar.Name,
		"listener", req.ListenerName, "rule", rule.Name).
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
	go notifySlack(sidecar, rev, req.ListenerName, string(statement))

	// Forward is false: the review was filed a moment ago and no human has
	// seen it. The sidecar denies this statement and carries the review id.
	c.JSON(http.StatusCreated, &openapi.SidecarReviewResponse{
		Forward: false,
		Review:  toOpenApiSidecarReview(rev),
	})
}

// answerExistingReview answers a retry against the review already filed for its
// statement, and a waiting sidecar's claim of it by id (ClaimReview). PENDING,
// REJECTED, REVOKED and EXECUTED come back as they stand, so a
// rejection keeps denying instead of being retried into a fresh review.
// APPROVED is claimed here, and only the claim's winner may forward.
func answerExistingReview(c *gin.Context, sidecar *models.Sidecar, listenerName string, rev *models.Review) {
	forward := false
	if rev.Status == models.ReviewStatusApproved {
		claimed, status, err := models.ClaimApprovedSidecarReview(models.DB, sidecar.OrgID, rev.ID)
		if err != nil {
			httputils.AbortWithErr(c, http.StatusInternalServerError, err,
				"failed consuming the approved sidecar review")
			return
		}
		// The status the row holds now, so a claim loser reports EXECUTED
		// rather than the APPROVED it read a moment earlier. It is forward,
		// not this, that says whether the statement may run.
		rev.Status = status
		forward = claimed

		if claimed {
			log.With("sid", rev.SessionID, "review-id", rev.ID, "sidecar", sidecar.Name,
				"listener", listenerName).
				Infof("consumed an approved sidecar review")

			trackClient := analytics.New()
			defer trackClient.Close()
			trackClient.TrackEvent(analytics.EventConsumeSidecarReview, map[string]any{
				"org-id":   sidecar.OrgID,
				"sidecar":  sidecar.Name,
				"listener": listenerName,
			})
		}
	}

	c.JSON(http.StatusOK, &openapi.SidecarReviewResponse{
		Forward: forward,
		Review:  toOpenApiSidecarReview(rev),
	})
}

// notifySlack posts the review to the org's Slack channel, so a human learns it
// exists rather than finding it by looking.
//
// Slack is optional everywhere else in this codebase and stays optional here:
// an org that has not configured it gets no message and no error.
func notifySlack(sidecar *models.Sidecar, rev *models.Review, listenerName, statement string) {
	slackSvc := slackservice.GetServiceInstance(sidecar.OrgID)
	if slackSvc == nil {
		return
	}

	// A sidecar review has no connection to take channels from, so the org
	// default is its only destination. Said out loud because otherwise a
	// misconfigured org gets silence that looks like success.
	if slackSvc.DefaultChannel() == "" {
		log.With("sid", rev.SessionID, "review-id", rev.ID).
			Warnf("the org has no default slack channel, nobody was notified of this review")
		return
	}

	req := newSlackReviewRequest(sidecar, rev, listenerName, statement)
	// The same ceiling both existing senders apply. Two groups today, but a
	// message with no buttons is a notification nobody can act on.
	if len(req.ApprovalGroups) == 0 || len(req.ApprovalGroups) >= slackplugin.SlackMaxButtons {
		log.With("review-id", rev.ID).Warnf("not sending a review message with %d approval groups",
			len(req.ApprovalGroups))
		return
	}

	result := slackSvc.SendMessageReview(req)
	log.With("sid", rev.SessionID, "review-id", rev.ID).Infof("slack review message, %v", result)
}

// newSlackReviewRequest is what a reviewer ends up reading. Split out so the
// message can be asserted without a Slack workspace.
func newSlackReviewRequest(sidecar *models.Sidecar, rev *models.Review, listenerName, statement string) *slackservice.MessageReviewRequest {
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
		Connection:     listenerName,
		ConnectionType: reviewSlackType,

		ApprovalGroups: slackplugin.ParseGroups(rev.ReviewGroups),
		Script:         statement,

		// FullApiURL, not ApiURL: the latter drops the configured path prefix,
		// which lands the approver outside the app wherever one is set.
		WebappURL: fmt.Sprintf("%s/reviews/%s", appconfig.Get().FullApiURL(), rev.SessionID),
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

	// Against the COMPOSED document, which is the one this sidecar was served.
	// A listener's analyzer block can come from a rule an admin bound in the
	// control plane, and composition places it on the way out without storing
	// it -- so the stored row's listener names no approval rule, while the
	// sidecar is running one and filing reviews under it. Authorizing against
	// the stored row would refuse every one of them, and the statement would
	// be denied forever with nothing saying why.
	//
	// Composition failing is not an authorization answer: it means the rules
	// could not be read or do not fit together, so it is reported rather than
	// rendered as a refusal the admin would look for in their reviewer list.
	listeners := sidecar.Configuration.Listeners
	if db != nil {
		composed, err := services.ComposeSidecarConfiguration(db, sidecar)
		if err != nil {
			return nil, fmt.Errorf("failed composing the sidecar configuration: %w", err)
		}
		listeners = composed.Listeners
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

// listenerNamesApprovalRule reports whether the configuration SERVED to this
// sidecar gives this listener this approval rule. It takes the listeners
// rather than the sidecar, and is free of the database, so the whole
// authorization table can be asserted without Postgres -- and so the caller
// cannot forget that the document to read is the composed one.
func listenerNamesApprovalRule(listeners []daemon.ListenerConfig, listenerName, ruleName string) bool {
	if listenerName == "" || ruleName == "" {
		return false
	}
	var analyzer *daemon.LaneAnalyzerConfig
	matches := 0
	for _, listener := range listeners {
		if listener.Name != listenerName {
			continue
		}
		matches++
		analyzer = listener.Analyzer
	}

	// Listener names are NOT unique: daemon validation keys uniqueness on the
	// listen address. Two lanes may share a name and name different rules, and
	// the request says which listener but not which lane, so authorizing
	// against either one would let the sidecar choose the looser of the two.
	if matches != 1 {
		return false
	}

	// A listener with no analyzer block, or one naming a different rule,
	// authorizes nothing. There is no inherited default: the people who may
	// release a statement against one database are not the people who may
	// release one against another.
	return analyzer != nil && analyzer.ApprovalRule == ruleName
}

// createSidecarReview writes the session and the review one statement needs to
// wait for a human. It returns gorm.ErrDuplicatedKey when a racing request
// filed for the same bytes first.
func createSidecarReview(sidecar *models.Sidecar, listenerName, statement, statementHash string, rule *models.AccessRequestRule, policy *services.ReviewPolicy) (*models.Review, error) {
	now := time.Now().UTC()
	sessionID := uuid.NewString()

	// A review is one per session (private.reviews is UNIQUE on org and
	// session), and UpdateReview syncs the session's status when the review
	// settles, so the session is not optional bookkeeping.
	sess := models.Session{
		ID:             sessionID,
		OrgID:          sidecar.OrgID,
		BlobInput:      models.BlobInputType(statement),
		Connection:     "",
		ConnectionType: reviewConnectionType,
		Verb:           pb.ClientVerbExec,
		Status:         string(openapi.SessionStatusOpen),
		UserID:         sidecar.ID,
		UserName:       sidecar.Name,
		UserEmail:      reviewOwnerEmail,
		CreatedAt:      now,
	}

	rev := newSidecarReview(sidecar, listenerName, sessionID, statementHash, rule, policy, now)
	if err := models.CreateSidecarReview(models.DB, sess, rev, statement); err != nil {
		return nil, err
	}
	return rev, nil
}

// newSidecarReview builds the row, and with it the whole approval policy the
// named rule describes. Nothing in the review path spells that out, so an
// omission here is silent: without MinApprovals the review needs every group,
// and without a group row a role cannot approve at all.
func newSidecarReview(sidecar *models.Sidecar, listenerName, sessionID, statementHash string, rule *models.AccessRequestRule, policy *services.ReviewPolicy, now time.Time) *models.Review {
	return &models.Review{
		ID:        uuid.NewString(),
		OrgID:     sidecar.OrgID,
		Type:      models.ReviewTypeOneTime,
		Status:    models.ReviewStatusPending,
		SessionID: sessionID,

		// The listener this statement arrived on, in place of a connection.
		// connection_name is NOT NULL and stays empty: a sidecar review never
		// resolves one, and a listener name in that column could collide with
		// a real connection of the same name.
		SidecarID:    sql.NullString{String: sidecar.ID, Valid: true},
		ListenerName: sql.NullString{String: listenerName, Valid: listenerName != ""},

		// What a retry of this statement matches on, and the reason the same
		// bytes are never reviewed twice while this review is live.
		StatementHash: sql.NullString{String: statementHash, Valid: statementHash != ""},

		OwnerID:    sidecar.ID,
		OwnerEmail: reviewOwnerEmail,
		OwnerName:  ptr.String(sidecar.Name),

		MinApprovals:          &policy.MinApprovals,
		ReviewGroups:          policy.Groups,
		ForceApprovalGroups:   rule.ForceApprovalGroups,
		AccessRequestRuleName: &rule.Name,

		CreatedAt: now,
	}
}

// toOpenApiSidecarReview renders what the sidecar needs to recognise the review
// later. The connection fields are left out rather than sent empty: this review
// has none, and an empty string invites a client to read meaning into it.
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
	}
}
