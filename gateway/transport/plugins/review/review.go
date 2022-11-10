package review

import (
	"fmt"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/plugin"
	"github.com/runopsio/hoop/gateway/user"
	"log"
)

const (
	name string = "review"
)

type (
	reviewPlugin struct {
		name    string
		storage storage
	}

	review struct {
		Id        string `edn:"xt/id"`
		Org       string `edn:"review/org"`
		CreatedBy string `edn:"review/created-by"`
	}

	storage interface {
		FindAll(context *user.Context) ([]review, error)
		Persist(context)
	}
)

func New() *reviewPlugin {
	return &reviewPlugin{name: name}
}

func (r *reviewPlugin) Name() string {
	return r.name
}

func (r *reviewPlugin) OnStartup(config plugin.Config) error {
	return nil
}

func (r *reviewPlugin) OnConnect(config plugin.Config) error {
	log.Printf("session=%v | review noop | processing on-connect", config.SessionId)
	if config.Org == "" || config.SessionId == "" {
		return fmt.Errorf("failed processing audit plugin, missing org_id and session_id params")
	}

	return nil
}

func (r *reviewPlugin) OnReceive(sessionID string, config []string, pkt *pb.Packet) error {
	log.Printf("[%s] Review OnReceive plugin with config %v and pkt %v", sessionID, config, pkt)
	switch pb.PacketType(pkt.GetType()) {
	case pb.PacketExecWriteAgentStdinType:

		return nil
	default:
		return nil
	}

	return nil
}

func (r *reviewPlugin) OnDisconnect(config plugin.Config) error {
	return nil
}

func (r *reviewPlugin) OnShutdown() {}
