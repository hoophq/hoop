package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
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

func SessionProperties(
	ctx storagev2.Context,
	s models.Session,
) map[string]any {
	var finishedAt string
	if s.EndSession != nil {
		finishedAt = s.EndSession.String()
	}

	return map[string]any{
		"org-id":           s.OrgID,
		"session-id":       s.ID,
		"is-admin":         ctx.IsAdminUser(),
		"resource-type":    s.ConnectionType,
		"resource-subtype": s.ConnectionSubtype,
		"status":           s.Status,
		"session-type":     s.Verb,
		"gateway-version":  version.Get().Version,
		"agent-version":    "",
		"created-at":       s.CreatedAt.String(),
		"finished-at":      finishedAt,

		"access-request-activated":                "",
		"access-request-force-approval-activated": "",
		"access-request-minimum-approval":         "",
		"access-request-action-date":              "",

		"guardrails-activated":                "",
		"data-masking-activated":              "",
		"ai-session-analyzer-activated":       "",
		"ai-session-analyzer-identified-risk": "",
		"ai-session-analyzer-action":          "",
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
