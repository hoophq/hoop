package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/segmentio/analytics-go/v3"
)

type Segment struct {
	analytics.Client
	environmentName string
}

func getUserIDHash(userID string) string {
	hash := sha256.Sum256([]byte(userID))
	return hex.EncodeToString(hash[:])
}

func New() *Segment {
	if segmentApiKey == "" {
		return &Segment{}
	}
	return &Segment{
		Client:          analytics.New(segmentApiKey),
		environmentName: appconfig.Get().ApiHostname(),
	}
}

func (s *Segment) Close() {
	if s == nil || s.Client == nil {
		return
	}

	if err := s.Client.Close(); err != nil {
		log.Warnf("failed closing analytics client, err=%v", err)
		return
	}
}

// Go to DB and get everything we need to track a session

// TODO: refactor data masking, guardrails, access control
// TODO: check access request from connections table too
// TODO: we could check reviews table to find reviewed-at property and min-approvals, force groups
func sessionPropertiesFromDB(ctx *types.APIContext, sessionID string, sessionType string) (map[string]any, error) {
	sessionProps, err := models.GetSessionPropertiesByID(ctx.OrgID, sessionID)
	if err != nil {
		log.Errorf("failed to fetch session properties for session %s in org %s: %v", sessionID, ctx.OrgID, err)
		return nil, err
	}

	var finishedAt string
	if sessionProps.EndSession != nil {
		finishedAt = sessionProps.EndSession.String()
	}

	return map[string]any{
		"org-id":                   sessionProps.OrgID,
		"session-id":               sessionProps.ID,
		"is-admin":                 ctx.IsAdminUser(),
		"resource-type":            sessionProps.ConnectionType,
		"resource-subtype":         sessionProps.ConnectionSubtype,
		"status":                   sessionProps.Status,
		"session-type":             "TODO: cli, webapp, etc",
		"gateway-version":          version.Get().Version,
		"agent-version":            sessionProps.AgentVersion,
		"created-at":               sessionProps.CreatedAt.String(),
		"finished-at":              finishedAt,
		"access-request-activated": sessionProps.AccessRequestActivated,
		"access-request-force-approval-activated": sessionProps.AccessRequestForceApprovals != nil && len(sessionProps.AccessRequestForceApprovals) > 0,
		"access-request-minimum-approval":         sessionProps.AccessRequestMinApprovals,
		"access-request-action-date":              "reviews table",
		"jira-template-activated":                 sessionProps.JiraIssueTemplateID.String != "",
		"guardrails-activated":                    sessionProps.GuardrailsActivated,
		"data-masking-activated":                  "datamasking_rules_connections checking status active",
		"ai-session-analyzer-activated":           "sessions table -> ai_analysis column",
		"ai-session-analyzer-identified-risk":     "sessions table -> ai_analysis column",
		"ai-session-analyzer-action":              "sessions table -> ai_analysis column",
	}, err
}

// Not checking the DB, it just receives all dependencies through input
func sessionProperties(ctx *types.APIContext, s models.Session, conn models.Connection, guardRailRules *models.ConnectionGuardRailRules, accessRequestRules *models.AccessRequestRule) map[string]any {
	var (
		finishedAt                          string
		jiraTemplateActivated               bool
		guardrailsActivated                 bool
		accessRequestActivated              bool
		accessRequestForceApprovalActivated bool
		accessRequestMinimumApproval        *int
	)
	if s.EndSession != nil {
		finishedAt = s.EndSession.String()
	}

	if conn.JiraIssueTemplateID.String != "" {
		jiraTemplateActivated = true
	}

	if guardRailRules != nil {
		guardrailsActivated = true
	}

	if accessRequestRules != nil {
		accessRequestActivated = true
		accessRequestForceApprovalActivated = accessRequestRules.ForceApprovalGroups != nil && len(accessRequestRules.ForceApprovalGroups) > 0
		accessRequestMinimumApproval = accessRequestRules.MinApprovals
	}

	return map[string]any{
		"org-id":                   s.OrgID,
		"session-id":               s.ID,
		"is-admin":                 ctx.IsAdminUser(),
		"resource-type":            s.ConnectionType,
		"resource-subtype":         s.ConnectionSubtype,
		"status":                   s.Status,
		"session-type":             s.Verb, //TODO: cli, webapp, etc
		"gateway-version":          version.Get().Version,
		"agent-version":            "",
		"created-at":               s.CreatedAt.String(),
		"finished-at":              finishedAt,
		"access-request-activated": accessRequestActivated,
		"access-request-force-approval-activated": accessRequestForceApprovalActivated,
		"access-request-minimum-approval":         accessRequestMinimumApproval,
		"access-request-action-date":              "",
		"jira-template-activated":                 jiraTemplateActivated,
		"guardrails-activated":                    guardrailsActivated,
		"data-masking-activated":                  "",
		"ai-session-analyzer-activated":           "",
		"ai-session-analyzer-identified-risk":     "",
		"ai-session-analyzer-action":              "",
	}
}

func (s *Segment) TrackSession(ctx *types.APIContext, eventName string, sessionID string) {
	sessionProps, err := sessionPropertiesFromDB(ctx, sessionID, "cli")
	if err != nil {
		return
	}

	s.Track(ctx.UserID, eventName, sessionProps)
}

func (s *Segment) Identify(ctx *types.APIContext) {
	if s.Client == nil || ctx == nil || ctx.UserID == "" || ctx.OrgID == "" ||
		!appconfig.Get().AnalyticsTracking() {
		return
	}

	hashedUserID := getUserIDHash(ctx.UserID)

	_ = s.Client.Enqueue(analytics.Identify{
		UserId:      hashedUserID,
		AnonymousId: ctx.UserAnonSubject,
		Traits: analytics.NewTraits().
			Set("org-id", ctx.OrgID).
			Set("user-id", hashedUserID).
			Set("is-admin", ctx.IsAdminUser()).
			Set("environment", s.environmentName).
			Set("status", ctx.UserStatus).
			Set("client-version", version.Get().Version),
	})

	_ = s.Client.Enqueue(analytics.Group{
		GroupId:     ctx.OrgID,
		AnonymousId: ctx.UserAnonSubject,
		UserId:      hashedUserID,
		Traits: analytics.NewTraits().
			Set("org-id", ctx.OrgID),
	})
}

// AnonymousTrack generates an event to segment using
// an anonymous id that then can be used to identify
// the user with the function MergeIdentifiedUserTrack
// references:
// - https://segment.com/docs/connections/spec/best-practices-identify/#anonymousid-generation
// - https://segment.com/docs/connections/spec/best-practices-identify/#merging-identified-and-anonymous-user-profiles
func (s *Segment) AnonymousTrack(anonymousId, eventName string, properties map[string]any) {
	if s.Client == nil || !appconfig.Get().AnalyticsTracking() {
		return
	}
	if properties == nil {
		properties = map[string]any{}
	}
	properties["environment"] = s.environmentName
	properties["auth-method"] = appconfig.Get().AuthMethod()
	properties["client-version"] = version.Get().Version

	_ = s.Enqueue(analytics.Track{
		AnonymousId: anonymousId,
		Event:       eventName,
		Properties:  properties,
	})
}

func (s *Segment) TrackEvent(eventName string, properties map[string]any) {
	if s.Client == nil || !appconfig.Get().AnalyticsTracking() {
		return
	}
	if properties == nil {
		properties = map[string]any{}
	}

	if apiUrl, exists := properties["api-hostname"]; (!exists || apiUrl == "") && appconfig.Get().ApiURL() != "" {
		url, err := url.Parse(appconfig.Get().ApiURL())
		if err == nil {
			properties["api-hostname"] = url.Hostname()
		}
	}

	if orgID, exists := properties["org-id"]; exists && orgID != "" {
		properties["$groups"] = map[string]any{
			"org-id": orgID,
		}
	}

	properties["auth-method"] = appconfig.Get().AuthMethod()
	properties["client-version"] = version.Get().Version

	_ = s.Client.Enqueue(analytics.Track{
		UserId:     "None",
		Event:      eventName,
		Properties: properties,
	})
}

// Track generates an event to segment
func (s *Segment) Track(userID, eventName string, properties map[string]any) {
	if s.Client == nil || !appconfig.Get().AnalyticsTracking() {
		return
	}
	if properties == nil {
		properties = map[string]any{}
	}

	if apiUrl, exists := properties["api-hostname"]; (!exists || apiUrl == "") && appconfig.Get().ApiURL() != "" {
		url, err := url.Parse(appconfig.Get().ApiURL())
		if err == nil {
			properties["api-hostname"] = url.Hostname()
		}
	}

	if orgID, exists := properties["org-id"]; exists && orgID != "" {
		properties["$groups"] = map[string]any{
			"org-id": orgID,
		}
	}

	hashedUserID := getUserIDHash(userID)

	properties["user-id"] = hashedUserID
	properties["auth-method"] = appconfig.Get().AuthMethod()
	properties["client-version"] = version.Get().Version

	_ = s.Client.Enqueue(analytics.Track{
		UserId:     hashedUserID,
		Event:      eventName,
		Properties: properties,
	})
}

// TODO: sample structure
func TrackSessionUsage(sessionType string) {

}
