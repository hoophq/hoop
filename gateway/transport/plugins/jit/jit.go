package jit

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/review/jit"
	"github.com/runopsio/hoop/gateway/user"
)

const (
	Name                     string = "jit"
	JitServiceParam          string = "jit_service"
	UserServiceParam         string = "user_service"
	NotificationServiceParam string = "notification_service"
)

type (
	jitPlugin struct {
		name                string
		apiURL              string
		jitService          JitService
		userService         UserService
		notificationService notification.Service
	}

	JitService interface {
		Persist(context *user.Context, review *jit.Jit) error
		FindBySessionID(sessionID string) (*jit.Jit, error)
	}

	UserService interface {
		FindByGroups(context *user.Context, groups []string) ([]user.User, error)
	}
)

func New(apiURL string) *jitPlugin {
	return &jitPlugin{
		name:   Name,
		apiURL: apiURL}
}

func (r *jitPlugin) Name() string {
	return r.name
}

func (r *jitPlugin) OnStartup(config plugin.Config) error {
	log.Printf("session=%v | jit | processing on-startup", config.SessionId)
	if config.Org == "" || config.SessionId == "" {
		return fmt.Errorf("failed processing review plugin, missing org_id and session_id params")
	}

	jitServiceParam := config.ParamsData[JitServiceParam]
	jitService, ok := jitServiceParam.(JitService)
	if !ok {
		return fmt.Errorf("jit plugin failed to start")
	}

	userServiceParam := config.ParamsData[UserServiceParam]
	userService, ok := userServiceParam.(UserService)
	if !ok {
		return fmt.Errorf("jit plugin failed to start")
	}

	notificationServiceParam := config.ParamsData[NotificationServiceParam]
	notificationService, ok := notificationServiceParam.(notification.Service)
	if !ok {
		return fmt.Errorf("jit plugin failed to start")
	}

	r.jitService = jitService
	r.userService = userService
	r.notificationService = notificationService

	return nil
}

func (r *jitPlugin) OnConnect(config plugin.Config) error {
	if config.Org == "" || config.SessionId == "" {
		return fmt.Errorf("failed processing jit plugin, missing org_id and session_id params")
	}

	return nil
}

func (r *jitPlugin) OnReceive(pluginConfig plugin.Config, config []string, pkt *pb.Packet) error {
	switch pb.PacketType(pkt.GetType()) {
	case pbagent.SessionOpen:
		context := &user.Context{
			Org:  &user.Org{Id: pluginConfig.Org},
			User: &user.User{Id: pluginConfig.UserID},
		}

		sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
		existingJit, err := r.jitService.FindBySessionID(sessionID)
		if err != nil {
			return err
		}

		connectDuration, err := time.ParseDuration(string(pkt.Spec[pb.SpecJitTimeout]))
		if err != nil {
			return fmt.Errorf("invalid duration input, found=%#v", string(pkt.Spec[pb.SpecJitTimeout]))
		}

		if existingJit != nil {
			if existingJit.Time == 0 && connectDuration != 0 {
				existingJit.Time = connectDuration
				if err := r.jitService.Persist(context, existingJit); err != nil {
					return err
				}
			}
			pkt.Spec[pb.SpecJitStatus] = []byte(existingJit.Status)
			pkt.Spec[pb.SpecGatewayJitID] = []byte(existingJit.Id)
			return nil
		}

		jitGroups := make([]jit.Group, 0)
		groups := make([]string, 0)
		for _, s := range config {
			groups = append(groups, s)
			jitGroups = append(jitGroups, jit.Group{
				Group:  s,
				Status: jit.StatusPending,
			})
		}

		jit := &jit.Jit{
			Id:      uuid.NewString(),
			Session: pluginConfig.SessionId,
			Connection: jit.Connection{
				Id:   pluginConfig.ConnectionId,
				Name: pluginConfig.ConnectionName,
			},
			Time:      connectDuration,
			Status:    jit.StatusPending,
			JitGroups: jitGroups,
		}

		if err := r.jitService.Persist(context, jit); err != nil {
			return err
		}

		pkt.Spec[pb.SpecJitStatus] = []byte(jit.Status)
		pkt.Spec[pb.SpecGatewayJitID] = []byte(jit.Id)
		reviewers, err := r.userService.FindByGroups(context, groups)
		if err != nil {
			return err
		}

		reviewersEmail := listEmails(reviewers)
		r.notificationService.Send(notification.Notification{
			Title:      "[hoop.dev] Pending just-in-time review",
			Message:    r.buildReviewUrl(jit.Id),
			Recipients: reviewersEmail,
		})
	}

	return nil
}

func (r *jitPlugin) OnDisconnect(config plugin.Config) error {
	return nil
}

func (r *jitPlugin) OnShutdown() {}

func (r *jitPlugin) buildReviewUrl(reviewID string) string {
	url := fmt.Sprintf("%s/plugins/jits/%s", r.apiURL, reviewID)
	return fmt.Sprintf("A user is waiting for your just-in-time review at hoop.dev.\n\n Visit %s for more information.\n\n Hoop Team.", url)
}

func listEmails(reviewers []user.User) []string {
	emails := make([]string, 0)
	for _, r := range reviewers {
		emails = append(emails, r.Email)
	}
	return emails
}
