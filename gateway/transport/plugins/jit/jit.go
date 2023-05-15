package jit

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	pbagent "github.com/runopsio/hoop/common/proto/agent"
	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/review/jit"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
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

func (r *jitPlugin) Name() string { return r.name }

func (r *jitPlugin) OnStartup(pctx plugintypes.Context) error {
	log.Printf("session=%v | jit | processing on-startup", pctx.SID)

	jitServiceParam := pctx.ParamsData[JitServiceParam]
	jitService, ok := jitServiceParam.(JitService)
	if !ok {
		return fmt.Errorf("jit plugin failed to start")
	}

	userServiceParam := pctx.ParamsData[UserServiceParam]
	userService, ok := userServiceParam.(UserService)
	if !ok {
		return fmt.Errorf("jit plugin failed to start")
	}

	notificationServiceParam := pctx.ParamsData[NotificationServiceParam]
	notificationService, ok := notificationServiceParam.(notification.Service)
	if !ok {
		return fmt.Errorf("jit plugin failed to start")
	}

	r.jitService = jitService
	r.userService = userService
	r.notificationService = notificationService

	return nil
}

func (r *jitPlugin) OnConnect(pctx plugintypes.Context) error {
	if pctx.ClientVerb != pb.ClientVerbConnect {
		return fmt.Errorf("connection subject to jit, use 'hoop connect %s' to interact", pctx.ConnectionName)
	}
	return nil
}

func (r *jitPlugin) OnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	switch pb.PacketType(pkt.GetType()) {
	case pbagent.SessionOpen:
		context := &user.Context{
			Org:  &user.Org{Id: pctx.OrgID},
			User: &user.User{Id: pctx.UserID},
		}

		existingJit, err := r.jitService.FindBySessionID(pctx.SID)
		if err != nil {
			return nil, err
		}

		connectDuration, err := time.ParseDuration(string(pkt.Spec[pb.SpecJitTimeout]))
		if err != nil {
			return nil, fmt.Errorf("invalid duration input, found=%#v", string(pkt.Spec[pb.SpecJitTimeout]))
		}

		if existingJit != nil {
			if existingJit.Time == 0 && connectDuration != 0 {
				existingJit.Time = connectDuration
				if err := r.jitService.Persist(context, existingJit); err != nil {
					return nil, err
				}
			}
			pkt.Spec[pb.SpecJitStatus] = []byte(existingJit.Status)
			pkt.Spec[pb.SpecGatewayJitID] = []byte(existingJit.Id)
			return nil, nil
		}

		if len(pctx.PluginConnectionConfig) == 0 {
			return nil, fmt.Errorf("connection does not have groups for the jit plugin")
		}
		jitGroups := make([]jit.Group, 0)
		groups := make([]string, 0)
		for _, s := range pctx.PluginConnectionConfig {
			groups = append(groups, s)
			jitGroups = append(jitGroups, jit.Group{
				Group:  s,
				Status: jit.StatusPending,
			})
		}

		jit := &jit.Jit{
			Id:      uuid.NewString(),
			Session: pctx.SID,
			Connection: jit.Connection{
				Id:   pctx.ConnectionID,
				Name: pctx.ConnectionName,
			},
			Time:      connectDuration,
			Status:    jit.StatusPending,
			JitGroups: jitGroups,
		}

		if err := r.jitService.Persist(context, jit); err != nil {
			return nil, err
		}

		pkt.Spec[pb.SpecJitStatus] = []byte(jit.Status)
		pkt.Spec[pb.SpecGatewayJitID] = []byte(jit.Id)
		reviewers, err := r.userService.FindByGroups(context, groups)
		if err != nil {
			return nil, err
		}

		reviewersEmail := listEmails(reviewers)
		r.notificationService.Send(notification.Notification{
			Title:      "[hoop.dev] Pending just-in-time review",
			Message:    r.buildReviewUrl(jit.Id),
			Recipients: reviewersEmail,
		})
	}

	return nil, nil
}

func (r *jitPlugin) OnDisconnect(_ plugintypes.Context, errMsg error) error { return nil }
func (r *jitPlugin) OnShutdown()                                            {}

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
