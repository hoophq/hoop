package jit

import (
	"fmt"
	"log"

	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/plugin"
)

const (
	Name string = "jit"
)

type (
	jitPlugin struct {
		name string
	}
)

func New() *jitPlugin {
	return &jitPlugin{name: Name}
}

func (r *jitPlugin) Name() string {
	return r.name
}

func (r *jitPlugin) OnStartup(config plugin.Config) error {
	log.Printf("session=%v | jit noop | processing on-startup", config.SessionId)
	if config.Org == "" || config.SessionId == "" {
		return fmt.Errorf("failed processing review plugin, missing org_id and session_id params")
	}

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
	log.Printf("[%s] Review OnReceive plugin with config %v and pkt %v", pluginConfig.SessionId, config, pkt)
	switch pb.PacketType(pkt.GetType()) {
	case pb.PacketClientGatewayConnectType:

	}

	return nil
}

func (r *jitPlugin) OnDisconnect(config plugin.Config) error {
	return nil
}

func (r *jitPlugin) OnShutdown() {}
