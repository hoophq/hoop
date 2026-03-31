package analytics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/segmentio/analytics-go/v3"
	"golang.org/x/sync/errgroup"
	"gorm.io/gorm"
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

	groups := make(map[string]any)

	if orgID, exists := properties["org-id"]; exists && orgID != "" {
		groups["org-id"] = orgID
	}

	if sessionID, exists := properties["session-id"]; exists && sessionID != "" {
		groups["session-id"] = sessionID
	}

	if len(groups) > 0 {
		properties["$groups"] = groups
	}

	hashedUserID := getUserIDHash(userID)

	properties["user-id"] = hashedUserID
	properties["auth-method"] = appconfig.Get().AuthMethod()
	properties["client-version"] = version.Get().Version

	if err := s.Client.Enqueue(analytics.Track{
		UserId:     hashedUserID,
		Event:      eventName,
		Properties: properties,
	}); err != nil {
		log.Warnf("failed to enqueue analytics event=%s, reason=%v", eventName, err)
	}
}

func sessionUsageProperties(s *models.Session, c *models.Connection, agent *models.Agent, guardrails *models.ConnectionGuardRailRules, dataMasking json.RawMessage, rules []*models.AccessRequestRule) map[string]any {
	props := map[string]any{
		"org-id":                        s.OrgID,
		"session-id":                    s.ID,
		"resource-type":                 s.ConnectionType,
		"resource-subtype":              s.ConnectionSubtype,
		"status":                        s.Status,
		"created-at":                    s.CreatedAt.String(),
		"ai-session-analyzer-activated": false,
		"agent-version":                 agent.GetMeta("version"),
		"agent-platform":                agent.GetMeta("platform"),
		"jira-template-activated":       c.JiraIssueTemplateID.Valid && c.JiraIssueTemplateID.String != "",
	}

	if s.EndSession != nil {
		props["finished-at"] = s.EndSession.String()
	}

	if s.AIAnalysis != nil {
		props["ai-session-analyzer-activated"] = true
		props["ai-session-analyzer-identified-risk"] = s.AIAnalysis.RiskLevel
		props["ai-session-analyzer-action"] = s.AIAnalysis.Action
	}

	return props
}

func (s *Segment) TrackSessionUsageData(eventName string, orgID string, userID string, sessionID string) {
	var (
		err                  error
		session              *models.Session
		connection           *models.Connection
		agent                *models.Agent
		guardrails           *models.ConnectionGuardRailRules
		jitAccessRequest     *models.AccessRequestRule
		commandAccessRequest *models.AccessRequestRule
		dataMasking          json.RawMessage
	)

	if session, err = models.GetSessionByID(orgID, sessionID); err != nil {
		log.Warnf("failed getting session by ID, reason=%v", err)
		return
	}

	if connection, err = models.GetConnectionByName(models.DB, session.Connection); err != nil {
		log.Warnf("failed getting connection features by name, reason=%v", err)
		return
	}

	group, _ := errgroup.WithContext(context.Background())
	group.Go(func() error {
		if agent, err = models.GetAgentByNameOrID(orgID, connection.AgentID.String); err != nil {
			log.Warnf("failed getting agent by name, reason=%v", err)
			return err
		}
		return nil
	})

	group.Go(func() error {
		if guardrails, err = services.GetGuardRailRulesForConnection(orgID, session.Connection); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warnf("failed getting guardrails for connection, reason=%v", err)
			return err
		}

		return nil
	})

	group.Go(func() error {
		if dataMasking, err = services.GetDataMaskingRulesForConnection(orgID, session.Connection); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warnf("failed getting data masking rules for connection, reason=%v", err)
			return err
		}

		return nil
	})

	group.Go(func() error {
		if jitAccessRequest, err = services.GetRuleForConnection(uuid.MustParse(orgID), session.Connection, "jit"); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warnf("failed getting jit rule for connection, reason=%v", err)
			return err
		}

		return nil
	})

	group.Go(func() error {
		if commandAccessRequest, err = services.GetRuleForConnection(uuid.MustParse(orgID), session.Connection, "command"); err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warnf("failed getting command rule for connection, reason=%v", err)
			return err
		}

		return nil
	})

	if err := group.Wait(); err != nil {
		return
	}

	props := sessionUsageProperties(session, connection, agent, guardrails, dataMasking, []*models.AccessRequestRule{jitAccessRequest, commandAccessRequest})
	log.With("sid", sessionID).Infof("tracking session usage data, event=%s, orgID=%s, userID=%s, props=%+v", eventName, orgID, userID, props)

	s.Track(userID, eventName, props)
}
