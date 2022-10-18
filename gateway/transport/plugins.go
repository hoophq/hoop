package transport

import (
	"log"

	"github.com/runopsio/hoop/common/memory"
	pluginscore "github.com/runopsio/hoop/common/plugins/core"
	pluginsaudit "github.com/runopsio/hoop/common/plugins/core/audit"
	"github.com/runopsio/hoop/gateway/user"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var pluginMemStore memory.Store

func init() {
	pluginMemStore = memory.New()
	auditPlugin := pluginsaudit.New()
	pluginMemStore.Set(auditPlugin.Name(), pluginsaudit.New())
}

type pluginConfig struct {
	enabled    bool
	configData pluginscore.ParamsData
}

func (c *pluginConfig) Enabled() bool {
	return c.enabled
}

func (c *pluginConfig) Config() pluginscore.ParamsData {
	return c.configData
}

func (s *Server) pluginOnConnectPhase(onConnectParams pluginscore.ParamsData, usrCtx *user.Context) error {
	for _, obj := range pluginMemStore.List() {
		plugin, ok := obj.(pluginscore.Plugin)
		if !ok {
			continue
		}
		p1, err := s.PluginService.FindOne(usrCtx, plugin.Name())
		if err != nil || p1 == nil {
			log.Printf("failed retrieving plugin %q, err=%v", plugin.Name(), err)
			return status.Errorf(codes.Internal, "failed registering plugins")
		}

		pconfig := &pluginConfig{configData: map[string]any{}}
		if plugin.Name() == pluginsaudit.Name {
			pconfig.configData["audit_storage_writer"] = s.SessionService.Storage.NewGenericStorageWriter()
		}

		// if the plugin matches the connection, the plugin is enabled
		for _, pconn := range p1.Connections {
			if pconn.Name == onConnectParams.GetString("connection_name") {
				pconfig.enabled = true
				break
			}
		}
		if err := plugin.OnStartup(pconfig); err != nil {
			log.Printf("failed starting plugin %q, err=%v", plugin.Name(), err)
			return status.Errorf(codes.Internal, "failed starting plugin")
		}
		err = plugin.OnConnect(onConnectParams)
		if err != nil {
			log.Printf("plugin %q refused to accept connection %q, err=%v",
				plugin.Name(), onConnectParams.GetString("session_id"), err)
			return status.Errorf(codes.FailedPrecondition, err.Error())
		}
	}
	return nil
}

func (s *Server) pluginOnReceivePhase(p pluginscore.ParamsData) error {
	for _, obj := range pluginMemStore.List() {
		plugin, ok := obj.(pluginscore.Plugin)
		if !ok {
			// WARN
			log.Printf("skipping, found a non-plugin in the store, type=%T, obj=%#v", obj, obj)
			continue
		}
		err := plugin.OnReceive(
			p.GetString("session_id"),
			p.GetString("packet_type"),
			p.GetByte("event_stream"))
		if err != nil {
			log.Printf("sessionid=%v - plugin %q rejected packet, err=%v",
				p.GetString("session_id"), plugin.Name(), err)
			return err
		}
	}
	return nil
}

func (s *Server) pluginOnDisconnectPhase(onDisconnectParams pluginscore.ParamsData) error {
	for _, obj := range pluginMemStore.List() {
		plugin, ok := obj.(pluginscore.Plugin)
		if !ok {
			// WARN
			log.Printf("skipping, found a non-plugin in the store, type=%T, obj=%#v", obj, obj)
			continue
		}
		return plugin.OnDisconnect(onDisconnectParams)
	}
	return nil
}
