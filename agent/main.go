package agent

import (
	"fmt"
	"hash/crc32"
	"os"
	"strings"
	"time"

	agentconfig "github.com/runopsio/hoop/agent/config"
	"github.com/runopsio/hoop/common/backoff"
	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/version"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	defaultUserAgent = fmt.Sprintf("hoopagent/%v", version.Get().Version)
	vi               = version.Get()
)

func tokenCrc32KeySuffix(keyName string) string {
	t := crc32.MakeTable(crc32.IEEE)
	return fmt.Sprintf("%s/%08x", keyName, crc32.Checksum([]byte(os.Getenv("HOOP_DSN")), t))
}

func Run() {
	config, err := agentconfig.Load()
	if err != nil {
		log.With("version", vi.Version).Fatal(err)
	}

	var environment string
	if hostname, _, found := strings.Cut(config.URL, ":"); found && config.Type == clientconfig.ModeDsn {
		environment = hostname
	}
	s3Logger := log.NewS3LoggerWriter("agent", environment, tokenCrc32KeySuffix(config.Name))
	if err := s3Logger.Init(); err != nil {
		log.With("version", vi.Version).Fatal(err)
	}
	defer s3Logger.Flush()
	// default to embedded mode if it's dsn type config to keep
	if config.Type == clientconfig.ModeDsn && config.AgentMode == pb.AgentModeEmbeddedType {
		RunV2(&pb.PreConnectRequest{}, nil)
		return
	}
	if err := runDefaultMode(s3Logger, config); err != nil {
		s3Logger.Flush()
		log.With("version", vi.Version).Fatal(err)
	}
}

// RunV2 should supersedes agent modes, instead of relying in two distincts modes of execution
// this method should deprecate old behaviors.
func RunV2(request *pb.PreConnectRequest, hostEnvs []string) {
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
			time.Sleep(time.Second * 10)
			continue
		}
		switch resp.Status {
		case pb.PreConnectStatusConnectType:
			runAgent(c, clientConfig)
		case pb.PreConnectStatusBackoffType:
			if resp.Message != "" {
				log.Infof("fail connecting to server, reason=%v", resp.Message)
			}
		default:
			log.Warnf("pre-connect status %q not implement", resp.Status)
		}
		time.Sleep(time.Second * 10)
	}
}

func runAgent(config *agentconfig.Config, clientConfig grpc.ClientConfig) {
	log.Infof("connecting to grpc server %v", config.URL)
	client, err := grpc.Connect(clientConfig, grpc.WithOption("origin", pb.ConnectionOriginAgent))
	if err != nil {
		log.Errorf("failed connecting to gateway, err=%v", err)
		return
	}
	defer client.Close()
	err = New(client, config, nil).Run()
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

func runDefaultMode(s3Log *log.S3LogWriter, config *agentconfig.Config) error {
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
		defer client.Close()
		err = New(client, config, s3Log).Run()
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
