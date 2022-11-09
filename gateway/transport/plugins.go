package transport

//var pluginMemStore memory.Store
//
//func init() {
//	pluginMemStore = memory.New()
//
//	auditPlugin := pluginsaudit.New()
//	reviewPlugin := pluginsreview.New()
//
//	pluginMemStore.Set(auditPlugin.Name(), auditPlugin)
//	pluginMemStore.Set(reviewPlugin.Name(), reviewPlugin)
//}

//type pluginConfig struct {
//	enabled    bool
//	configData plugins.ParamsData
//}
//
//func (c *pluginConfig) Enabled() bool {
//	return c.enabled
//}
//
//func (c *pluginConfig) Config() plugins.ParamsData {
//	return c.configData
//}

//func (s *Server) pluginOnConnectPhase(onConnectParams plugin.Config, ctx *user.Context) error {
//	for _, obj := range pluginMemStore.List() {
//		plugin, ok := obj.(plugins.Plugin)
//		if !ok {
//			continue
//		}
//		p1, err := s.PluginService.FindOne(ctx, plugin.Name())
//		if err != nil || p1 == nil {
//			log.Printf("failed retrieving plugin %q, err=%v", plugin.Name(), err)
//			return status.Errorf(codes.Internal, "failed registering plugins")
//		}
//
//		//pconfig := &pluginConfig{configData: map[string]any{}}
//if plugin.Name() == pluginsaudit.Name {
//	pconfig.configData["audit_storage_writer"] = s.SessionService.Storage.NewGenericStorageWriter()
//}
//
//		// if the plugin matches the connection, the plugin is enabled
//		for _, pconn := range p1.Connections {
//			if pconn.Name == onConnectParams.GetString("connection_name") {
//				pconfig.enabled = true
//				break
//			}
//		}
//		if err := plugin.OnStartup(pconfig); err != nil {
//			log.Printf("failed starting plugin %q, err=%v", plugin.Name(), err)
//			return status.Errorf(codes.Internal, "failed starting plugin")
//		}
//		err = plugin.OnConnect(onConnectParams)
//		if err != nil {
//			log.Printf("plugin %q refused to accept connection %q, err=%v",
//				plugin.Name(), onConnectParams.GetString("session_id"), err)
//			return status.Errorf(codes.FailedPrecondition, err.Error())
//		}
//	}
//	return nil
//}

//func (s *Server) pluginOnReceivePhase(sessionID string, pkt plugins.PacketData) error {
//	for _, obj := range pluginMemStore.List() {
//		plugin, ok := obj.(plugins.Plugin)
//		if !ok {
//			// WARN
//			log.Printf("skipping, found a non-plugin in the store, type=%T, obj=%#v", obj, obj)
//			continue
//		}
//		if err := plugin.OnReceive(sessionID, pkt); err != nil {
//			log.Printf("session=%v - plugin %q rejected packet, err=%v",
//				sessionID, plugin.Name(), err)
//			return err
//		}
//	}
//	return nil
//}
