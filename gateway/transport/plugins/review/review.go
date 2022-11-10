package review

import (
	"fmt"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/plugin"
	"log"
)

const (
	name string = "review"
)

type (
	reviewPlugin struct {
		name string
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
	case pb.PacketPGWriteServerType:
		return nil
	case pb.PacketExecClientWriteStdoutType:
		return nil
	case pb.PacketExecWriteAgentStdinType, pb.PacketExecRunProcType:
		return nil
	}

	return nil
}

func (r *reviewPlugin) OnDisconnect(config plugin.Config) error {
	return nil
}

func (r *reviewPlugin) OnShutdown() {}
