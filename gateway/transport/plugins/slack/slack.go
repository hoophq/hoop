package slack

import (
	"github.com/runopsio/hoop/gateway/plugin"

	pb "github.com/runopsio/hoop/common/proto"
)

const Name string = "slack"

type (
	slackPlugin struct {
		name string
	}
)

func New() *slackPlugin {
	return &slackPlugin{name: Name}
}

func (p *slackPlugin) Name() string {
	return p.name
}

func (p *slackPlugin) OnStartup(config plugin.Config) error {
	return nil
}

func (p *slackPlugin) OnConnect(config plugin.Config) error {
	return nil
}

func (p *slackPlugin) OnReceive(pluginConfig plugin.Config, config []string, pkt *pb.Packet) error {
	return nil
}

func (p *slackPlugin) OnDisconnect(config plugin.Config, errMsg error) error {
	return nil
}

func (p *slackPlugin) OnShutdown() {}
