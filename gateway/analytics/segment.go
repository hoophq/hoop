package analytics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

// resolveMode picks the analytics mode for a call. It prefers the mode
// already populated on the APIContext (set at auth time) and falls back to
// the in-memory cache for callers that build an APIContext manually
// (signup, login bootstrap).
func resolveMode(ctx *types.APIContext) string {
	if ctx == nil {
		return models.AnalyticsModeAnonymous
	}
	if models.IsValidAnalyticsMode(ctx.OrgAnalyticsMode) {
		return ctx.OrgAnalyticsMode
	}
	return GetMode(ctx.OrgID)
}

// identifyTraits builds the trait set for an Identify call. When the org is
// in `identified` mode the user's real email and name are attached so
// Intercom can address them; `anonymous` keeps the pseudonymous hashed
// user-id as the only identifier.
func identifyTraits(ctx *types.APIContext, mode, hashedUserID, environmentName string) analytics.Traits {
	traits := analytics.NewTraits().
		Set("org-id", ctx.OrgID).
		Set("user-id", hashedUserID).
		Set("is-admin", ctx.IsAdminUser()).
		Set("environment", environmentName).
		Set("status", ctx.UserStatus).
		Set("client-version", version.Get().Version)

	if mode == models.AnalyticsModeIdentified && ctx.UserEmail != "" {
		traits = traits.SetEmail(ctx.UserEmail).SetName(ctx.UserName)
	}

	return traits
}

func (s *Segment) Identify(ctx *types.APIContext) {
	if s.Client == nil || ctx == nil || ctx.UserID == "" || ctx.OrgID == "" {
		return
	}

	mode := resolveMode(ctx)
	if mode == models.AnalyticsModeDisabled {
		return
	}

	hashedUserID := getUserIDHash(ctx.UserID)

	_ = s.Client.Enqueue(analytics.Identify{
		UserId:      hashedUserID,
		AnonymousId: ctx.UserAnonSubject,
		Traits:      identifyTraits(ctx, mode, hashedUserID, s.environmentName),
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
	if s.Client == nil {
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
	if s.Client == nil {
		return
	}
	if properties == nil {
		properties = map[string]any{}
	}

	// System events (UserId="Gateway") have no user PII; if a specific org
	// is associated, respect its `disabled` choice so the org can fully
	// silence outbound telemetry.
	if orgID, _ := properties["org-id"].(string); orgID != "" {
		if GetMode(orgID) == models.AnalyticsModeDisabled {
			return
		}
		properties["$groups"] = map[string]any{
			"org-id": orgID,
		}
	}

	if apiUrl, exists := properties["api-hostname"]; (!exists || apiUrl == "") && appconfig.Get().ApiURL() != "" {
		url, err := url.Parse(appconfig.Get().ApiURL())
		if err == nil {
			properties["api-hostname"] = url.Hostname()
		}
	}

	properties["auth-method"] = appconfig.Get().AuthMethod()
	properties["client-version"] = version.Get().Version

	_ = s.Client.Enqueue(analytics.Track{
		UserId:     "Gateway",
		Event:      eventName,
		Properties: properties,
	})
}

// Track generates an event to segment
func (s *Segment) Track(userID, eventName string, properties map[string]any) {
	if s.Client == nil {
		return
	}
	if properties == nil {
		properties = map[string]any{}
	}

	if orgID, _ := properties["org-id"].(string); orgID != "" {
		if GetMode(orgID) == models.AnalyticsModeDisabled {
			return
		}
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

type sessionUsageData struct {
	session              *models.Session
	connection           *models.Connection
	agent                *models.Agent
	guardrails           *models.ConnectionGuardRailRules
	dataMasking          json.RawMessage
	jitAccessRequest     *models.AccessRequestRule
	commandAccessRequest *models.AccessRequestRule
}

func loadSessionUsageData(orgID, sessionID string) (*sessionUsageData, error) {
	session, err := models.GetSessionByID(orgID, sessionID)
	if err != nil {
		log.Warnf("failed getting session by ID, reason=%v", err)
		return nil, err
	}

	connection, err := models.GetConnectionByName(models.DB, session.Connection)
	if err != nil {
		log.Warnf("failed getting connection features by name, reason=%v", err)
		return nil, err
	}

	data := &sessionUsageData{session: session, connection: connection}
	orgUUID := uuid.MustParse(orgID)

	group, _ := errgroup.WithContext(context.Background())
	group.Go(func() error {
		agent, err := models.GetAgentByNameOrID(orgID, connection.AgentID.String)
		if err != nil {
			log.Warnf("failed getting agent by name, reason=%v", err)
			return err
		}
		data.agent = agent
		return nil
	})
	group.Go(func() error {
		guardrails, err := services.GetGuardRailRulesForConnection(orgID, session.Connection)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warnf("failed getting guardrails for connection, reason=%v", err)
			return err
		}
		data.guardrails = guardrails
		return nil
	})
	group.Go(func() error {
		dataMasking, err := services.GetDataMaskingRulesForConnection(orgID, session.Connection)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warnf("failed getting data masking rules for connection, reason=%v", err)
			return err
		}
		data.dataMasking = dataMasking
		return nil
	})
	group.Go(func() error {
		rule, err := services.GetRuleForConnection(orgUUID, session.Connection, "jit")
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warnf("failed getting jit rule for connection, reason=%v", err)
			return err
		}
		data.jitAccessRequest = rule
		return nil
	})
	group.Go(func() error {
		rule, err := services.GetRuleForConnection(orgUUID, session.Connection, "command")
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Warnf("failed getting command rule for connection, reason=%v", err)
			return err
		}
		data.commandAccessRequest = rule
		return nil
	})

	if err := group.Wait(); err != nil {
		return nil, err
	}
	return data, nil
}

func sessionUsageProperties(d *sessionUsageData) map[string]any {
	s, c := d.session, d.connection

	props := map[string]any{
		"org-id":                           s.OrgID,
		"session-id":                       s.ID,
		"resource-type":                    s.ConnectionType,
		"resource-subtype":                 s.ConnectionSubtype,
		"verb":                             s.Verb,
		// origin reflects what is persisted on the session; sessions created
		// before the column existed (or by paths that don't set it) report "".
		"origin": s.Origin,
		"status": s.Status,
		"created-at":                       s.CreatedAt.String(),
		"ai-session-analyzer-activated":    false,
		"agent-version":                    d.agent.GetMeta("version"),
		"agent-platform":                   d.agent.GetMeta("platform"),
		"mandatory-metadata-activated":     len(c.MandatoryMetadataFields) > 0,
		"jira-template-activated":          c.JiraIssueTemplateID.Valid && c.JiraIssueTemplateID.String != "",
		"jit-access-request-activated":     false,
		"command-access-request-activated": false,
		"guardrails-activated":             d.guardrails != nil && !d.guardrails.HasEmptyRules(),
		"data-masking-activated":           string(d.dataMasking) != "[]",
	}

	if s.EndSession != nil {
		props["finished-at"] = s.EndSession.String()
	}

	if s.AIAnalysis != nil {
		props["ai-session-analyzer-activated"] = true
		props["ai-session-analyzer-identified-risk"] = s.AIAnalysis.RiskLevel
		props["ai-session-analyzer-action"] = s.AIAnalysis.Action
	}

	for _, rule := range []*models.AccessRequestRule{d.jitAccessRequest, d.commandAccessRequest} {
		if rule == nil {
			continue
		}
		props[fmt.Sprintf("%s-access-request-activated", rule.AccessType)] = true
		props[fmt.Sprintf("%s-access-request-force-approval", rule.AccessType)] = len(rule.ForceApprovalGroups) > 0
		props[fmt.Sprintf("%s-access-request-all-groups-must-approve", rule.AccessType)] = rule.AllGroupsMustApprove
		if rule.MinApprovals != nil {
			props[fmt.Sprintf("%s-access-request-minimum-approval", rule.AccessType)] = *rule.MinApprovals
		}
	}

	return props
}

func (s *Segment) TrackSessionUsageData(eventName string, orgID string, userID string, sessionID string) {
	data, err := loadSessionUsageData(orgID, sessionID)
	if err != nil {
		return
	}
	log.With("sid", sessionID).Infof("tracking session usage data, event=%s", eventName)
	s.Track(userID, eventName, sessionUsageProperties(data))
}

// CreateConnectionEvent is the canonical input for the hoop-create-connection
// analytics event. Different connection-creation paths fill different subsets of
// these fields — empty/zero fields are omitted from the emitted properties.
type CreateConnectionEvent struct {
	// Always required
	OrgID   string
	Source  string // e.g. "connections-api", "resources-api", "mcp", "aws-rds-provisioner", "agent-autoregister"
	Type    string
	SubType string
	Command []string // first token used as the "command" property; empty -> ""

	// User-attributed paths set UserID. System paths leave it empty and the
	// event is emitted via TrackEvent (UserId="Gateway") instead.
	UserID      string
	LicenseType string

	// System / agent-bound metadata. Optional.
	AgentID   string
	ManagedBy string

	// HTTP-context metadata. APIHostname acts as the gating signal — if set,
	// the HTTP triple (content-length, user-agent, api-hostname) is included.
	ContentLength int64
	UserAgent     string
	APIHostname   string
}

// TrackCreateConnection emits hoop-create-connection with a consistent property
// shape across every connection-creation path. If evt.UserID is empty the event
// is emitted as a system event (UserId="Gateway") so dashboards can distinguish
// user-initiated from background-job creations via the "source" property.
func (s *Segment) TrackCreateConnection(evt CreateConnectionEvent) {
	properties := map[string]any{
		"org-id":      evt.OrgID,
		"auth-method": appconfig.Get().AuthMethod(),
		"source":      evt.Source,
		"type":        evt.Type,
		"subtype":     evt.SubType,
		"command":     "",
	}
	if len(evt.Command) > 0 {
		properties["command"] = evt.Command[0]
	}
	if evt.LicenseType != "" {
		properties["license-type"] = evt.LicenseType
	}
	if evt.AgentID != "" {
		properties["agent-id"] = evt.AgentID
	}
	if evt.ManagedBy != "" {
		properties["managed-by"] = evt.ManagedBy
	}
	if evt.APIHostname != "" {
		properties["content-length"] = evt.ContentLength
		properties["user-agent"] = evt.UserAgent
		properties["api-hostname"] = evt.APIHostname
	}

	if evt.UserID == "" {
		s.TrackEvent(EventCreateConnection, properties)
		return
	}
	s.Track(evt.UserID, EventCreateConnection, properties)
}
