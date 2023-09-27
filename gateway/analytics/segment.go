package analytics

import (
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"github.com/segmentio/analytics-go/v3"
)

var envName = getEnvironment()

type Segment struct {
	analytics.Client
}

func New() *Segment { return &Segment{analytics.New("IuHRCK0Q9fdliDdgjQDddfrPFRG0X0RA")} }
func (s *Segment) Identify(ctx *types.APIContext) {
	if s.Client == nil || ctx == nil || ctx.UserID == "" || ctx.OrgID == "" ||
		envName == "127.0.0.1" || envName == "localhost" {
		return
	}

	_ = s.Client.Enqueue(analytics.Identify{
		UserId: ctx.UserID,
		Traits: analytics.NewTraits().
			SetName(ctx.UserName).
			SetEmail(ctx.UserEmail).
			Set("groups", ctx.UserGroups).
			Set("is-admin", ctx.IsAdminUser()).
			Set("environment", envName).
			Set("status", ctx.UserStatus),
	})

	orgName := ctx.OrgName
	if orgName == pb.DefaultOrgName {
		// use the name of the environment on self-hosted setups
		orgName = envName
	}
	_ = s.Client.Enqueue(analytics.Group{
		GroupId: ctx.OrgID,
		UserId:  ctx.UserID,
		Traits: analytics.NewTraits().
			SetName(orgName),
	})
}

// Track generates an event to segment, if the context is not set, it will emit an anoynimous event
func (s *Segment) Track(ctx *types.APIContext, eventName string, properties map[string]any) {
	if s.Client == nil || envName == "127.0.0.1" || envName == "localhost" {
		return
	}
	if properties == nil {
		properties = map[string]any{}
	}
	properties["environment"] = envName
	if ctx == nil || ctx.UserID == "" {
		_ = s.Client.Enqueue(analytics.Track{
			AnonymousId: uuid.NewString(),
			Event:       eventName,
			Properties:  properties,
		})
		return
	}
	properties["email"] = ctx.UserEmail
	_ = s.Client.Enqueue(analytics.Track{
		UserId:     ctx.UserID,
		Event:      eventName,
		Properties: properties,
	})
}

// getEnvironment uses the API_URL as a unique identifier to track events
// In practice this should be unique to multiple installations.
func getEnvironment() string {
	apiURL := os.Getenv("API_URL")
	if u, _ := url.Parse(apiURL); u != nil {
		return u.Hostname()
	}
	environment := strings.TrimPrefix(apiURL, "http://")
	return strings.TrimPrefix(environment, "https://")
}
