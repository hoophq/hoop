package analytics

import (
	"github.com/runopsio/hoop/gateway/user"
	"github.com/segmentio/analytics-go/v3"
	"os"
)

type (
	Segment struct {
		analytics.Client
	}

	Analytics interface {
		Track(userID, eventName string, properties map[string]any)
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
	if s.Client == nil {
		return
	}

	_ = s.Client.Enqueue(analytics.Identify{
		UserId: ctx.User.Id,
		Traits: analytics.NewTraits().
			SetName(ctx.User.Name).
			SetEmail(ctx.User.Email).
			Set("groups", ctx.User.Groups).
			Set("isAdmin", ctx.User.IsAdmin()).
			Set("status", ctx.User.Status),
	})

	_ = s.Client.Enqueue(analytics.Group{
		GroupId: ctx.Org.Id,
		UserId:  ctx.User.Id,
		Traits: analytics.NewTraits().
			SetName(ctx.Org.Name),
	})
}

func (s *Segment) Track(userID, eventName string, properties map[string]any) {
	if s.Client == nil {
		return
	}

	_ = s.Client.Enqueue(analytics.Track{
		UserId:     userID,
		Event:      eventName,
		Properties: properties,
	})
}
