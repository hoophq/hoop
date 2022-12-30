package jit

import (
	"errors"
	"fmt"
	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/review/jit"
	"github.com/runopsio/hoop/gateway/user"
	"log"
	"strconv"
	"time"
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
		return fmt.Errorf("failed processing review plugin, missing org_id and session_id params")
	}

	return nil
}

func (r *jitPlugin) OnReceive(pluginConfig plugin.Config, config []string, pkt *pb.Packet) error {
	switch pb.PacketType(pkt.GetType()) {
	case pb.PacketClientGatewayConnectType:
		context := &user.Context{
			Org:  &user.Org{Id: pluginConfig.Org},
			User: &user.User{Id: pluginConfig.UserID},
		}

		sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
		existingJit, err := r.jitService.FindBySessionID(sessionID)
		if err != nil {
			return err
		}

		var timeInt int
		requestTime := pkt.Spec[pb.SpecJitTimeout]
		if requestTime != nil {
			timeInt, err = strconv.Atoi(string(requestTime))
			if err != nil {
				return errors.New("invalid just-in-time plugin configuration")
			}
		}

		if existingJit != nil {
			if existingJit.Time == 0 && timeInt != 0 {
				existingJit.Time = time.Duration(timeInt)
				if err := r.jitService.Persist(context, existingJit); err != nil {
					return err
				}
			}
			b, _ := pb.GobEncode(existingJit)
			pkt.Spec[pb.SpecJitDataKey] = b
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
			Time:      time.Duration(timeInt),
			Status:    jit.StatusPending,
			JitGroups: jitGroups,
		}

		if err := r.jitService.Persist(context, jit); err != nil {
			return err
		}

		b, _ := pb.GobEncode(jit)
		pkt.Spec[pb.SpecJitDataKey] = b

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
