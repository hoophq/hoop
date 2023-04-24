package review

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/plugin"
	rv "github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/user"
)

const (
	Name                     string = "review"
	ReviewServiceParam       string = "review_service"
	UserServiceParam         string = "user_service"
	NotificationServiceParam string = "notification_service"
)

type (
	reviewPlugin struct {
		name                string
		apiURL              string
		reviewService       ReviewService
		userService         UserService
		notificationService notification.Service
	}

	ReviewService interface {
		Persist(context *user.Context, review *rv.Review) error
		FindBySessionID(sessionID string) (*rv.Review, error)
	}

	UserService interface {
		FindByGroups(context *user.Context, groups []string) ([]user.User, error)
	}
)

func New(apiURL string) *reviewPlugin {
	return &reviewPlugin{
		name:   Name,
		apiURL: apiURL,
	}
}

func (r *reviewPlugin) Name() string {
	return r.name
}

func (r *reviewPlugin) OnStartup(config plugin.Config) error {
	log.Printf("session=%v | review | processing on-startup", config.SessionId)
	if config.Org == "" || config.SessionId == "" {
		return fmt.Errorf("failed processing review plugin, missing org_id and session_id params")
	}

	reviewServiceParam := config.ParamsData[ReviewServiceParam]
	reviewService, ok := reviewServiceParam.(ReviewService)
	if !ok {
		return fmt.Errorf("review plugin failed to start")
	}

	userServiceParam := config.ParamsData[UserServiceParam]
	userService, ok := userServiceParam.(UserService)
	if !ok {
		return fmt.Errorf("review plugin failed to start")
	}

	notificationServiceParam := config.ParamsData[NotificationServiceParam]
	notificationService, ok := notificationServiceParam.(notification.Service)
	if !ok {
		return fmt.Errorf("review plugin failed to start")
	}

	r.reviewService = reviewService
	r.userService = userService
	r.notificationService = notificationService
	return nil
}

func (r *reviewPlugin) OnConnect(config plugin.Config) error {
	log.Printf("session=%v | review noop | processing on-connect", config.SessionId)
	if config.Org == "" || config.SessionId == "" {
		return fmt.Errorf("failed processing review plugin, missing org_id and session_id params")
	}
	if config.Verb != pb.ClientVerbExec {
		if config.ConnectionType != pb.ConnectionTypeCommandLine {
			return fmt.Errorf("the review plugin can't be used for this connection type, contact the administrator")
		}
		return fmt.Errorf("connection subject to review, use 'hoop exec %s' to interact", config.ConnectionName)
	}
	return nil
}

func (r *reviewPlugin) OnReceive(pluginConfig plugin.Config, config []string, pkt *pb.Packet) error {
	switch pkt.Type {
	case pbagent.SessionOpen:
		context := &user.Context{
			Org:  &user.Org{Id: pluginConfig.Org},
			User: &user.User{Id: pluginConfig.UserID},
		}

		sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
		existingReview, err := r.reviewService.FindBySessionID(sessionID)
		if err != nil {
			return err
		}

		if existingReview != nil {
			b, _ := pb.GobEncode(existingReview)
			pkt.Spec[pb.SpecReviewDataKey] = b
			return nil
		}

		reviewGroups := make([]rv.Group, 0)
		groups := make([]string, 0)
		for _, s := range config {
			groups = append(groups, s)
			reviewGroups = append(reviewGroups, rv.Group{
				Group:  s,
				Status: rv.StatusPending,
			})
		}

		review := &rv.Review{
			Id:      uuid.NewString(),
			Session: pluginConfig.SessionId,
			Connection: rv.Connection{
				Id:   pluginConfig.ConnectionId,
				Name: pluginConfig.ConnectionName,
			},
			Input:        string(pkt.Payload),
			Status:       rv.StatusPending,
			ReviewGroups: reviewGroups,
		}

		if err := r.reviewService.Persist(context, review); err != nil {
			return err
		}

		b, _ := pb.GobEncode(review)
		pkt.Spec[pb.SpecReviewDataKey] = b

		reviewers, err := r.userService.FindByGroups(context, groups)
		if err != nil {
			return err
		}

		reviewersEmail := listEmails(reviewers)
		r.notificationService.Send(notification.Notification{
			Title:      "[hoop.dev] Pending review",
			Message:    r.buildReviewUrl(review.Id),
			Recipients: reviewersEmail,
		})

	}

	return nil
}

func (r *reviewPlugin) OnDisconnect(config plugin.Config, errMsg error) error { return nil }

func (r *reviewPlugin) OnShutdown() {}

func (r *reviewPlugin) buildReviewUrl(reviewID string) string {
	url := fmt.Sprintf("%s/plugins/reviews/%s", r.apiURL, reviewID)
	return fmt.Sprintf("A user is waiting for your review at hoop.dev.\n\n Visit %s for more information.\n\n Hoop Team.", url)
}

func listEmails(reviewers []user.User) []string {
	emails := make([]string, 0)
	for _, r := range reviewers {
		emails = append(emails, r.Email)
	}
	return emails
}
