package streamclient

import (
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/models"
	pluginsslack "github.com/hoophq/hoop/gateway/transport/plugins/slack"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type runtimePlugin struct {
	plugintypes.Plugin
	config []string
}

func loadRuntimePlugins(ctx plugintypes.Context) ([]runtimePlugin, error) {
	pluginsConfig := make([]runtimePlugin, 0)
	if ctx.ClientVerb == pb.ClientVerbPlainExec {
		return pluginsConfig, nil
	}
	var nonRegisteredPlugins []string
	for _, p := range plugintypes.RegisteredPlugins {
		p1, err := models.GetPluginByName(ctx.GetOrgID(), p.Name())
		if err != nil && err != models.ErrNotFound {
			log.Errorf("failed retrieving plugin %q, err=%v", p.Name(), err)
			return nil, status.Errorf(codes.Internal, "failed registering plugins")
		}
		if p1 == nil {
			nonRegisteredPlugins = append(nonRegisteredPlugins, p.Name())
			continue
		}

		if p.Name() == plugintypes.PluginSlackName {
			if p1.EnvVars != nil {
				ctx.ParamsData[pluginsslack.PluginConfigEnvVarsParam] = p1.EnvVars
			}
		}

		for _, c := range p1.Connections {
			if c.ConnectionName == ctx.ConnectionName {
				config := removePluginConfigDuplicates(c.Config)
				ep := runtimePlugin{
					Plugin: p,
					config: config,
				}

				if err = p.OnConnect(ctx); err != nil {
					log.Warnf("plugin %q refused to accept connection %q, err=%v", p1.Name, ctx.SID, err)
					return pluginsConfig, status.Errorf(codes.FailedPrecondition, err.Error())
				}

				pluginsConfig = append(pluginsConfig, ep)
				break
			}
		}
	}
	if len(nonRegisteredPlugins) > 0 {
		log.With("sid", ctx.SID).Infof("non registered plugins %v", nonRegisteredPlugins)
	}
	return pluginsConfig, nil
}

func (s *ProxyStream) PluginExecOnReceive(pctx plugintypes.Context, pkt *pb.Packet) (*plugintypes.ConnectResponse, error) {
	var response *plugintypes.ConnectResponse
	for _, p := range s.runtimePlugins {
		pctx.PluginConnectionConfig = p.config
		resp, err := p.OnReceive(pctx, pkt)
		if err != nil {
			return nil, err
		}
		if resp != nil && response == nil {
			response = resp
		}
	}
	return response, nil
}

func (s *ProxyStream) PluginExecOnDisconnect(ctx plugintypes.Context, errMsg error) error {
	for _, p := range s.runtimePlugins {
		if err := p.OnDisconnect(ctx, errMsg); err != nil {
			return err
		}
	}
	return nil
}

// GetRedactInfoTypes return the in memory info types for the data masking (dlp) plugin
func (s *ProxyStream) GetRedactInfoTypes() []string {
	var infoTypes []string
	for _, p := range s.runtimePlugins {
		if p.Plugin.Name() == plugintypes.PluginDLPName {
			infoTypes = p.config
		}
	}
	return infoTypes
}

func removePluginConfigDuplicates(strSlice []string) []string {
	allKeys := make(map[string]bool)
	list := make([]string, 0)
	for _, item := range strSlice {
		if _, value := allKeys[item]; !value {
			allKeys[item] = true
			list = append(list, item)
		}
	}
	return list
}
