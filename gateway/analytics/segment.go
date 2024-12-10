package analytics

import (
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/segmentio/analytics-go/v3"
)

var environmentName = appconfig.Get().ApiHostname()

type Segment struct {
	analytics.Client
}

func New() *Segment {
	if segmentApiKey == "" {
		return &Segment{}
	}
	return &Segment{analytics.New(segmentApiKey)}
}

func (s *Segment) Identify(ctx *types.APIContext) {
	if s.Client == nil || ctx == nil || ctx.UserEmail == "" || ctx.OrgID == "" ||
		appconfig.Get().DoNotTrack() {
		return
	}

	_ = s.Client.Enqueue(analytics.Identify{
		// Segment recommends using an unique id for user id
		// However we use the e-mail to avoid having to associate the
		// user id with the e-mail.
		UserId: ctx.UserEmail,
		AnonymousId: ctx.UserAnonSubject,
		Traits: analytics.NewTraits().
			SetName(ctx.UserName).
			SetEmail(ctx.UserEmail).
			Set("groups", ctx.UserGroups).
			Set("is-admin", ctx.IsAdminUser()).
			Set("environment", environmentName).
			Set("status", ctx.UserStatus),
	})

	orgName := ctx.OrgName
	if orgName == pb.DefaultOrgName {
		// use the name of the environment on self-hosted setups
		orgName = environmentName
	}
	_ = s.Client.Enqueue(analytics.Group{
		GroupId: ctx.OrgID,
		UserId:  ctx.UserID,
		Traits: analytics.NewTraits().
			SetName(orgName),
	})
}

// AnonymousTrack generates an event to segment using
// an anonymous id that then can be used to identify
// the user with the function MergeIdentifiedUserTrack
// references:
// - https://segment.com/docs/connections/spec/best-practices-identify/#anonymousid-generation
// - https://segment.com/docs/connections/spec/best-practices-identify/#merging-identified-and-anonymous-user-profiles
func (s *Segment) AnonymousTrack(anonymousId, eventName string, properties map[string]any) {
	if s.Client == nil || appconfig.Get().DoNotTrack() {
		return
	}
	if properties == nil {
		properties = map[string]any{}
	}
	properties["environment"] = environmentName
	properties["auth-method"] = appconfig.Get().AuthMethod()
	properties["api-url"] = appconfig.Get().ApiURL()

	_ = s.Enqueue(analytics.Track{
		AnonymousId: anonymousId,
		Event:      eventName,
		Properties: properties,
	})
}

// Track generates an event to segment
func (s *Segment) Track(userEmail, eventName string, properties map[string]any) {
	if s.Client == nil || appconfig.Get().DoNotTrack() {
		return
	}
	if properties == nil {
		properties = map[string]any{}
	}
	properties["environment"] = environmentName
	properties["email"] = userEmail
	properties["auth-method"] = appconfig.Get().AuthMethod()
	properties["api-url"] = appconfig.Get().ApiURL()
	_ = s.Client.Enqueue(analytics.Track{
		UserId:     userEmail,
		Event:      eventName,
		Properties: properties,
	})
}
