package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"
	agentconfig "github.com/runopsio/hoop/agent/config"
	"github.com/runopsio/hoop/agent/controller"
	"github.com/runopsio/hoop/common/backoff"
	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/version"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	defaultUserAgent = fmt.Sprintf("hoopagent/%v", version.Get().Version)
	vi               = version.Get()
	agentStore       = memory.New()
	agentInstanceKey = "instance"
)

func Run() {
	config, err := agentconfig.Load()
	if err != nil {
		log.With("version", vi.Version).Fatal(err)
	}

	// default to embedded mode if it's dsn type config to keep
	if config.Type == clientconfig.ModeDsn && config.AgentMode == pb.AgentModeEmbeddedType {
		RunV2(&pb.PreConnectRequest{}, nil)
		return
	}
	handleOsInterrupt()
	if err := runDefaultMode(config); err != nil {
		log.With("version", vi.Version).Fatal(err)
	}
}

// RunV2 should supersedes agent modes, instead of relying in two distincts modes of execution
// this method should deprecate old behaviors.
func RunV2(request *pb.PreConnectRequest, hostEnvs []string) {
	handleOsInterrupt()
	c, err := agentconfig.Load()
	if err != nil {
		log.With("version", vi.Version).Fatal(err)
	}
	clientConfig, err := c.GrpcClientConfig()
	if err != nil {
		log.With("version", vi.Version).Fatal(err)
	}
	clientConfig.UserAgent = defaultUserAgent
	log.Infof("version=%v, platform=%v, type=%v, grpc_server=%v, tls=%v, strict-tls=%v - starting agent",
		vi.Version, vi.Platform, c.Type, c.URL, !c.IsInsecure(), vi.StrictTLS)

	// TODO: handle go routine ending for graceful shutdown
	for {
		resp, err := grpc.PreConnectRPC(clientConfig, request)
		if err != nil {
			log.With("version", vi.Version).Infof("failed pre-connect, reason=%v", err)
			time.Sleep(time.Second * 5)
			continue
		}
		log.Debugf("pre-connect rpc, status=%v, message=%v", resp.Status, resp.Message)
		switch resp.Status {
		case pb.PreConnectStatusConnectType:
			runAgent(c, clientConfig, request.Name)
		case pb.PreConnectStatusBackoffType:
			if resp.Message != "" {
				log.Infof("fail connecting to server, reason=%v", resp.Message)
			}
		default:
			log.Warnf("pre-connect status %q not implement", resp.Status)
		}
		time.Sleep(time.Second * 5)
	}
}

func runAgent(config *agentconfig.Config, clientConfig grpc.ClientConfig, connectionName string) {
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
	ctrl := controller.New(client, config)
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
	log.Infof("version=%v, platform=%v, type=%v, mode=%v, grpc_server=%v, tls=%v, strict-tls=%v - starting agent",
		vi.Version, vi.Platform, config.Type, config.AgentMode, config.URL, !config.IsInsecure(), vi.StrictTLS)

	return backoff.Exponential2x(func(v time.Duration) error {
		log.With("version", vi.Version, "backoff", v.String()).
			Infof("connecting to %v, tls=%v", clientConfig.ServerAddress, !config.IsInsecure())
		client, err := grpc.Connect(clientConfig, grpc.WithOption("origin", pb.ConnectionOriginAgent))
		if err != nil {
			log.With("version", vi.Version, "backoff", v.String()).
				Warnf("failed to connect to %s, reason=%v", config.URL, err.Error())
			return backoff.Error()
		}
		ctrl := controller.New(client, config)
		agentStore.Set(agentInstanceKey, ctrl)
		defer func() { agentStore.Del(agentInstanceKey); ctrl.Close(nil) }()
		err = ctrl.Run()
		var grpcStatusCode = codes.Code(99)
		if status, ok := status.FromError(err); ok {
			grpcStatusCode = status.Code()
		}
		switch grpcStatusCode {
		case codes.Canceled:
			// reset backoff
			log.With("version", vi.Version, "backoff", v.String()).Infof("context canceled")
			return nil
		case codes.Unauthenticated:
			log.With("version", vi.Version, "backoff", v.String()).Infof("unauthenticated")
			return backoff.Error()
		}
		log.With("version", vi.Version, "backoff", v.String(), "status", grpcStatusCode).
			Infof("disconnected from %v, reason=%v", config.URL, err)
		return backoff.Error()
	})
}

func handleOsInterrupt() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)
	go func() {
		sigval := <-sigc
		msg := fmt.Sprintf("received signal '%v' from the operating system", sigval)
		log.Debugf(msg)
		obj := agentStore.Pop(agentInstanceKey)
		instance, _ := obj.(*controller.Agent)
		cleanExit := true
		if instance != nil {
			timeoutCtx, timeoutCancelFn := context.WithTimeout(context.Background(), time.Second*10)
			go func() { instance.Close(errors.New(msg)); timeoutCancelFn() }()
			<-timeoutCtx.Done()
			if err := timeoutCtx.Err(); err == context.DeadlineExceeded {
				cleanExit = false
				log.Warnf("timeout (10s) waiting for agent to close graceful")
			}
		}
		sentry.Flush(time.Second * 2)
		log.With("clean-exit", cleanExit).Debugf("exiting program")
		switch sigval {
		case syscall.SIGHUP:
			os.Exit(int(syscall.SIGHUP))
		case syscall.SIGINT:
			os.Exit(int(syscall.SIGINT))
		case syscall.SIGTERM:
			os.Exit(int(syscall.SIGTERM))
		case syscall.SIGQUIT:
			os.Exit(int(syscall.SIGQUIT))
		}
	}()
}
