package review

import (
	"fmt"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/plugin"
	rv "github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/user"
	"log"
)

const (
	Name         string = "review"
	ServiceParam string = "review_service"
)

type (
	reviewPlugin struct {
		name    string
		service Service
	}

	Service interface {
		Persist(context *user.Context, review *rv.Review) error
	}
)

func New() *reviewPlugin {
	return &reviewPlugin{name: Name}
}

func (r *reviewPlugin) Name() string {
	return r.name
}

func (r *reviewPlugin) OnStartup(config plugin.Config) error {
	log.Printf("session=%v | review noop | processing on-startup", config.SessionId)
	if config.Org == "" || config.SessionId == "" {
		return fmt.Errorf("failed processing review plugin, missing org_id and session_id params")
	}

	reviewServiceParam := config.ParamsData[ServiceParam]
	reviewService, ok := reviewServiceParam.(Service)
	if !ok {
		return fmt.Errorf("review plugin failed to start")
	}

	r.service = reviewService
	return nil
}

func (r *reviewPlugin) OnConnect(config plugin.Config) error {
	log.Printf("session=%v | review noop | processing on-connect", config.SessionId)
	if config.Org == "" || config.SessionId == "" {
		return fmt.Errorf("failed processing review plugin, missing org_id and session_id params")
	}

	return nil
}

func (r *reviewPlugin) OnReceive(pluginConfig plugin.Config, config []string, pkt *pb.Packet) error {
	log.Printf("[%s] Review OnReceive plugin with config %v and pkt %v", pluginConfig.SessionId, config, pkt)
	switch pb.PacketType(pkt.GetType()) {
	case pb.PacketTerminalWriteAgentStdinType:
		reviewGroups := make([]rv.Group, 0)

		for _, s := range config {
			reviewGroups = append(reviewGroups, rv.Group{
				Group:  s,
				Status: rv.StatusPending,
			})
		}

		review := &rv.Review{
			Session: pluginConfig.SessionId,
			Connection: rv.Connection{
				Id:   pluginConfig.ConnectionId,
				Name: pluginConfig.ConnectionName,
			},
			Command:      string(pkt.Payload),
			Status:       rv.StatusPending,
			ReviewGroups: reviewGroups,
		}

		if err := r.service.Persist(&user.Context{
			Org:  &user.Org{Id: pluginConfig.Org},
			User: &user.User{Id: pluginConfig.User},
		}, review); err != nil {
			return err
		}
	}

	return nil
}

func (r *reviewPlugin) OnDisconnect(config plugin.Config) error {
	return nil
}

func (r *reviewPlugin) OnShutdown() {}
