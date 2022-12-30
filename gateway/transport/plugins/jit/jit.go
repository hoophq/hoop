package jit

import (
	"fmt"
	"github.com/runopsio/hoop/gateway/notification"
	rv "github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/user"
	"log"

	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/plugin"
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
		reviewService       JitService
		userService         UserService
		notificationService notification.Service
	}

	JitService interface {
		Persist(context *user.Context, review *rv.Review) error
		FindBySessionID(sessionID string) (*rv.Review, error)
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
	log.Printf("session=%v | jit noop | processing on-startup", config.SessionId)
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

	r.reviewService = jitService
	r.userService = userService
	r.notificationService = notificationService

	return nil
}

func (r *jitPlugin) OnConnect(config plugin.Config) error {
	log.Printf("session=%v | jit noop | processing on-connect", config.SessionId)
	if config.Org == "" || config.SessionId == "" {
		return fmt.Errorf("failed processing review plugin, missing org_id and session_id params")
	}

	return nil
}

func (r *jitPlugin) OnReceive(pluginConfig plugin.Config, config []string, pkt *pb.Packet) error {
	log.Printf("[%s] Jit OnReceive plugin with config %v and pkt %v", pluginConfig.SessionId, config, pkt)
	switch pb.PacketType(pkt.GetType()) {
	case pb.PacketClientGatewayConnectType:

	}

	return nil
}

func (r *jitPlugin) OnDisconnect(config plugin.Config) error {
	return nil
}

func (r *jitPlugin) OnShutdown() {}
