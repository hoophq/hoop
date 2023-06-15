package analytics

import (
	"os"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/runopsio/hoop/gateway/user"
	"github.com/segmentio/analytics-go/v3"
)

type (
	Segment struct {
		analytics.Client
	}
)

func New() *Segment {
	key := os.Getenv("SEGMENT_KEY")
	if key == "" {
		return &Segment{}
	}

	client := analytics.New(key)
	return &Segment{client}
}

func (s *Segment) Identify(ctx *user.Context) {
	if s.Client == nil || ctx == nil || ctx.User == nil || ctx.Org == nil {
		return
	}

	_ = s.Client.Enqueue(analytics.Identify{
		UserId: ctx.User.Id,
		Traits: analytics.NewTraits().
			SetName(ctx.User.Name).
			SetEmail(ctx.User.Email).
			Set("groups", ctx.User.Groups).
			Set("is-admin", ctx.User.IsAdmin()).
			Set("status", ctx.User.Status),
	})

	_ = s.Client.Enqueue(analytics.Group{
		GroupId: ctx.Org.Id,
		UserId:  ctx.User.Id,
		Traits: analytics.NewTraits().
			SetName(ctx.Org.Name),
	})
}

// Track generates an event to segment, if the context is not set, it will emit an anoynimous event
func (s *Segment) Track(ctx *types.APIContext, eventName string, properties map[string]any) {
	if s.Client == nil {
		return
	}
	if ctx == nil || ctx.UserID == "" {
		_ = s.Client.Enqueue(analytics.Track{
			AnonymousId: uuid.NewString(),
			Event:       eventName,
			Properties:  properties,
		})
		return
	}
	if properties == nil {
		properties = map[string]any{}
	}
	properties["email"] = ctx.UserEmail
	_ = s.Client.Enqueue(analytics.Track{
		UserId:     ctx.UserID,
		Event:      eventName,
		Properties: properties,
	})
}
