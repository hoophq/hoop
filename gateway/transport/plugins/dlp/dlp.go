package dlp

import (
	"github.com/runopsio/hoop/gateway/plugin"

	"encoding/base64"

	pb "github.com/runopsio/hoop/common/proto"
)

const Name string = "dlp"

type (
	dlpPlugin struct {
		name string
	}
)

func New() *dlpPlugin {
	base64.StdEncoding.Decode(nil, nil)
	return &dlpPlugin{name: Name}
}

func (p *dlpPlugin) Name() string {
	return p.name
}

func (p *dlpPlugin) OnStartup(config plugin.Config) error {
	return nil
}

func (p *dlpPlugin) OnConnect(config plugin.Config) error {
	return nil
}

func (p *dlpPlugin) OnReceive(pluginConfig plugin.Config, config []string, pkt *pb.Packet) error {
	return nil
}

func (p *dlpPlugin) OnDisconnect(config plugin.Config) error {
	return nil
}

func (p *dlpPlugin) OnShutdown() {}
