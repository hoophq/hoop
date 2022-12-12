package access_control

import (
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/gateway/plugin"
)

const (
	Name string = "access_control"
)

type (
	accessControlPlugin struct {
		name string
	}
)

func New() *accessControlPlugin {
	return &accessControlPlugin{name: Name}
}

func (r *accessControlPlugin) Name() string {
	return r.name
}

func (r *accessControlPlugin) OnStartup(config plugin.Config) error {
	return nil
}

func (r *accessControlPlugin) OnConnect(config plugin.Config) error {
	return nil
}

func (r *accessControlPlugin) OnReceive(pluginConfig plugin.Config, config []string, pkt *pb.Packet) error {
	return nil
}

func (r *accessControlPlugin) OnDisconnect(config plugin.Config) error {
	return nil
}

func (r *accessControlPlugin) OnShutdown() {}
