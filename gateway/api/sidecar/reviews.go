package apisidecar

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
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
//	@Success		201						{object}	openapi.Review
//	@Failure		400,401,412,413,422,500	{object}	openapi.HTTPError
//	@Router			/sidecars/reviews [post]
func PostReview(c *gin.Context) {
	sidecar := apiroutes.SidecarFromContext(c)
	if sidecar == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"message": "access denied"})
		return
	}

	// A gateway would take the review and then never be able to settle it:
	// approval there resolves a connection, and this review has none. Refuse
	// rather than write a row nobody can act on. Checked after authentication,
	// so an unauthenticated caller learns nothing about the deployment.
	if !appconfig.Get().IsControlPlane() {
		c.JSON(http.StatusPreconditionFailed, gin.H{
			"message": "sidecar reviews are served by the control plane",
		})
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

	rule, err := authorizedApprovalRule(sidecar, req.ListenerName, req.ApprovalRule)
	if err != nil {
		var refusal ruleNotAuthorized
		if errors.As(err, &refusal) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": refusal.Error()})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed loading the approval rule")
		return
	}

	// A rule naming nobody produces a review with no group row, which no
	// approval can ever settle. EVL-286 stores a sidecar rule without checking
	// its reviewer settings, so this is the first place it is checked.
	policy, err := services.ReviewPolicyFromRule(sidecar.OrgID, rule)
	if err != nil {
		if errors.Is(err, services.ErrRuleHasNoReviewers) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
			return
		}
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed building the review policy")
		return
	}

	rev, err := createSidecarReview(sidecar, req.ListenerName, string(statement), rule, policy)
	if err != nil {
		// The error is logged and sent to Sentry by AbortWithErr; the caller
		// gets none of it. A database message names constraints, tables and
		// columns, and a token holder has no use for any of that.
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed creating sidecar review")
		return
	}

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

	c.JSON(http.StatusCreated, toOpenApiSidecarReview(rev))
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

		// /reviews/<sid> exists in the router but renders a placeholder, so
		// this points at the home page until there is a layout to point at.
		// FullApiURL, not ApiURL: the latter drops the configured path prefix,
		// which lands the approver outside the app wherever one is set.
		WebappURL: appconfig.Get().FullApiURL(),
	}
}

// authorizedApprovalRule loads the rule the request names and checks that the
// calling sidecar may file against it.
//
// Authorization comes from the sidecar's STORED configuration, never from the
// body: the request could otherwise name any rule in the organization and pick
// its own approvers. The listener must exist in that configuration and its
// analyzer block must name this rule (EVL-294).
func authorizedApprovalRule(sidecar *models.Sidecar, listenerName, ruleName string) (*models.AccessRequestRule, error) {
	refuse := ruleNotAuthorized{listenerName: listenerName, ruleName: ruleName}

	if !listenerNamesApprovalRule(sidecar, listenerName, ruleName) {
		return nil, refuse
	}

	orgID, err := uuid.Parse(sidecar.OrgID)
	if err != nil {
		return nil, fmt.Errorf("failed parsing the sidecar organization id: %w", err)
	}

	rule, err := models.GetAccessRequestRuleByName(models.DB, ruleName, orgID)
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

// listenerNamesApprovalRule reports whether the sidecar's stored configuration
// gives this listener this approval rule. Split out, and free of the database,
// so the whole authorization table can be asserted without Postgres.
func listenerNamesApprovalRule(sidecar *models.Sidecar, listenerName, ruleName string) bool {
	if listenerName == "" || ruleName == "" {
		return false
	}
	for _, listener := range sidecar.Configuration.Listeners {
		if listener.Name != listenerName {
			continue
		}
		// A listener with no analyzer block, or one naming a different rule,
		// authorizes nothing. There is no inherited default: the people who
		// may release a statement against one database are not the people who
		// may release one against another.
		return listener.Analyzer != nil && listener.Analyzer.ApprovalRule == ruleName
	}
	return false
}

// createSidecarReview writes the session and the review one statement needs to
// wait for a human.
func createSidecarReview(sidecar *models.Sidecar, listenerName, statement string, rule *models.AccessRequestRule, policy *services.ReviewPolicy) (*models.Review, error) {
	now := time.Now().UTC()
	sessionID := uuid.NewString()

	// A review is one per session (private.reviews is UNIQUE on org and
	// session), and UpdateReview syncs the session's status when the review
	// settles, so the session is not optional bookkeeping.
	err := models.UpsertSession(models.Session{
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
	})
	if err != nil {
		return nil, fmt.Errorf("failed creating session: %w", err)
	}

	rev := newSidecarReview(sidecar, listenerName, sessionID, rule, policy, now)
	if err := models.CreateReview(rev, statement); err != nil {
		return nil, fmt.Errorf("failed creating review: %w", err)
	}
	return rev, nil
}

// newSidecarReview builds the row, and with it the whole approval policy the
// named rule describes. Nothing in the review path spells that out, so an
// omission here is silent: without MinApprovals the review needs every group,
// and without a group row a role cannot approve at all.
func newSidecarReview(sidecar *models.Sidecar, listenerName, sessionID string, rule *models.AccessRequestRule, policy *services.ReviewPolicy, now time.Time) *models.Review {
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
