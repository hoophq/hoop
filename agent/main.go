package agent

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	llog "libhoop/llog"

	agentconfig "github.com/hoophq/hoop/agent/config"
	"github.com/hoophq/hoop/agent/controller"
	"github.com/hoophq/hoop/common/backoff"
	"github.com/hoophq/hoop/common/clientconfig"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/common/monitoring"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/common/version"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	defaultUserAgent       = fmt.Sprintf("hoopagent/%v", version.Get().Version)
	vi                     = version.Get()
	agentStore             = memory.New()
	agentInstanceKey       = "instance"
	defaultBackoffResetSec = 60 // 1min
)

func Run() {
	// Reinitialize libhoop logger to use the same log format configured in client
	llog.ReinitializeLogger()

	_, _ = monitoring.StartSentry()
	config, err := agentconfig.Load()
	if err != nil {
		log.With("version", vi.Version).Fatal(err)
	}

	// default to embedded mode if it's dsn type config to keep
	if config.Type == clientconfig.ModeDsn && config.AgentMode == pb.AgentModeEmbeddedType {
		RunV2(&pb.PreConnectRequest{}, nil, nil)
		return
	}
	handleOsInterrupt(nil)
	if err := runDefaultMode(config); err != nil {
		log.With("version", vi.Version).Fatal(err)
	}
}

// RunV2 should supersedes agent modes, instead of relying in two distincts modes of execution
// this method should deprecate old behaviors.
func RunV2(req *pb.PreConnectRequest, runtimeEnvs map[string]string, commandArgs []string) {
	if runtimeEnvs == nil {
		runtimeEnvs = map[string]string{}
	}
	c, err := agentconfig.Load()
	if err != nil {
		log.With("version", vi.Version).Fatal(err)
	}
	clientConfig, err := c.GrpcClientConfig()
	if err != nil {
		log.With("version", vi.Version).Fatal(err)
	}
	log.With("version", vi.Version).Infof("agent started, args=%v", len(commandArgs))
	log.Debugf("version=%v, platform=%v, type=%v, mode=%v, grpc_server=%v, tls=%v, tlsca=%v - starting agent",
		vi.Version, vi.Platform, c.Type, c.AgentMode, c.URL, !c.IsInsecure(), c.HasTlsCA())
	clientConfig.UserAgent = defaultUserAgent
	cmd := newCommand(runtimeEnvs, commandArgs)
	handleOsInterrupt(func() {
		if err := killProcess(cmd); err != nil {
			log.With("version", vi.Version).Debug(err)
		}
	})
	for key, val := range req.Envs {
		runtimeEnvs[key] = val
	}
	// do not sync, use the runtime environment variables instead
	req.Envs = nil

	stopFn := runAgentController(c, clientConfig, req, runtimeEnvs)
	if len(commandArgs) == 0 {
		// block forever until it receives
		// kill signal from the operating system
		select {}
	}
	exitCode := 0
	err = cmd.Run()
	if err != nil {
		if exit, _ := err.(*exec.ExitError); exit != nil {
			exitCode = exit.ExitCode()
		}
		log.Warnf("foreground command terminated (%v) with error: %v", exitCode, err)
	}
	log.Debugf("foreground command terminated, exit_code=%v, err=%v", exitCode, err)
	stopFn()
	_ = cleanupAgentInstance(nil, nil)
}

func runAgentController(conf *agentconfig.Config, cc grpc.ClientConfig, req *pb.PreConnectRequest, runtimeEnvs map[string]string) context.CancelFunc {
	ctx, cancelFn := context.WithCancel(context.Background())
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			resp, err := grpc.PreConnectRPC(cc, req)
			if err != nil {
				log.With("version", vi.Version).Infof("failed pre-connect, reason=%v", err)
				time.Sleep(time.Second * 5)
				continue
			}
			log.Debugf("pre-connect rpc, status=%v, message=%v", resp.Status, resp.Message)
			switch resp.Status {
			case pb.PreConnectStatusConnectType:
				runAgent(conf, cc, req.Name, runtimeEnvs)
			case pb.PreConnectStatusBackoffType:
				if resp.Message != "" {
					log.Infof("fail connecting to server, reason=%v", resp.Message)
				}
			default:
				log.Warnf("pre-connect status %q not implement", resp.Status)
			}
			time.Sleep(time.Second * 5)
		}
	}()
	return cancelFn
}

func runAgent(config *agentconfig.Config, clientConfig grpc.ClientConfig, connectionName string, runtimeEnvs map[string]string) {
	log.Infof("connecting to grpc server %v", config.URL)
	grpcOptions := []*grpc.ClientOptions{grpc.WithOption("origin", pb.ConnectionOriginAgent)}
	if connectionName != "" {
		grpcOptions = append(grpcOptions, grpc.WithOption("connection-name", connectionName))
	}
	client, err := grpc.Connect(clientConfig, grpcOptions...)
	if err != nil {
		log.Errorf("failed connecting to gateway, err=%v", err)
		return
	}
	ctrl := controller.New(client, config, runtimeEnvs)
	agentStore.Set(agentInstanceKey, ctrl)
	defer func() { agentStore.Del(agentInstanceKey); ctrl.Close(nil) }()
	err = ctrl.Run()
	// err = New(client, config).Run()
	var grpcStatusCode = codes.Code(99)
	if status, ok := status.FromError(err); ok {
		grpcStatusCode = status.Code()
	}
	switch grpcStatusCode {
	case codes.Canceled:
		log.With("version", vi.Version).Infof("context canceled")
	case codes.Unauthenticated:
		log.With("version", vi.Version).Infof("unauthenticated")
	default:
		log.With("version", vi.Version, "status", grpcStatusCode).Infof("disconnected from %v, reason=%v", config.URL, err)
	}
}

func runDefaultMode(config *agentconfig.Config) error {
	clientConfig, err := config.GrpcClientConfig()
	if err != nil {
		return err
	}
	clientConfig.UserAgent = defaultUserAgent
	log.Infof("version=%v, platform=%v, type=%v, mode=%v, grpc_server=%v, tls=%v, tlsca=%v - starting agent",
		vi.Version, vi.Platform, config.Type, config.AgentMode, config.URL, !config.IsInsecure(), config.HasTlsCA())

	return backoff.Exponential2x(func(v time.Duration) error {
		log.With("version", vi.Version, "backoff", v.String()).
			Infof("connecting to %v, tls=%v", clientConfig.ServerAddress, !config.IsInsecure())
		client, err := grpc.Connect(clientConfig, grpc.WithOption("origin", pb.ConnectionOriginAgent))
		if err != nil {
			log.With("version", vi.Version, "backoff", v.String()).
				Warnf("failed to connect to %s, reason=%v", config.URL, err.Error())
			return backoff.Error()
		}
		ctrl := controller.New(client, config, nil)
		agentStore.Set(agentInstanceKey, ctrl)
		defer func() { agentStore.Del(agentInstanceKey); ctrl.Close(nil) }()
		connectStart := time.Now().UTC()
		err = ctrl.Run()
		// reset backoff if it has been connected for more than the specified time
		backoffErr, elapsed := backoff.Error(), int(time.Now().UTC().Sub(connectStart).Seconds())
		if elapsed > defaultBackoffResetSec {
			log.With("version", vi.Version, "current-backoff", v.String(), "connect-time-in-sec", elapsed).Infof("reseting backoff")
			backoffErr = nil
		}
		var grpcStatusCode = codes.Code(99)
		if status, ok := status.FromError(err); ok {
			grpcStatusCode = status.Code()
		}
		switch grpcStatusCode {
		case codes.Canceled:
			// always reset on this status
			log.With("version", vi.Version, "backoff", v.String()).Infof("context canceled")
			return nil
		case codes.Unauthenticated:
			log.With("version", vi.Version, "backoff", v.String()).Infof("unauthenticated")
			return backoffErr
		}
		log.With("version", vi.Version, "backoff", v.String(), "status", grpcStatusCode).
			Infof("disconnected from %v, reason=%v", config.URL, err)
		return backoffErr
	})
}
