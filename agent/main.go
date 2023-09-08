package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	agentconfig "github.com/runopsio/hoop/agent/config"
	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/runopsio/hoop/common/version"
)

var defaultUserAgent = fmt.Sprintf("hoopagent/%v", version.Get().Version)

func Run() {
	config, err := agentconfig.Load()
	if err != nil {
		log.Fatal(err)
	}
	// default to embedded mode if it's dsn type config to keep
	// compatibility with old client keys that doesn't have the mode attribute param
	if config.Type == clientconfig.ModeDsn &&
		config.AgentMode == pb.AgentModeEmbeddedType || config.AgentMode == "" {
		runEmbeddedMode(config)
		return
	}
	runDefaultMode(config)
}

func runDefaultMode(config *agentconfig.Config) {
	clientOptions := []*grpc.ClientOptions{grpc.WithOption("origin", pb.ConnectionOriginAgent)}
	clientConfig, err := config.GrpcClientConfig()
	if err != nil {
		log.Fatal(err)
	}
	clientConfig.UserAgent = defaultUserAgent
	vs := version.Get()
	log.Infof("version=%v, platform=%v, type=%v, mode=%v, grpc_server=%v, tls=%v, strict-tls=%v - starting agent",
		vs.Version, vs.Platform, config.Type, config.AgentMode, config.URL, !config.IsInsecure(), vs.StrictTLS)

	client, err := grpc.Connect(clientConfig, clientOptions...)
	if err != nil {
		log.Fatalf("failed to connect to %s, err=%v", config.URL, err.Error())
	}
	err = New(client, config).Run()
	if err != io.EOF {
		log.Fatalf("disconnected from %v, err=%v", config.URL, err.Error())
	}
	log.Warnf("disconnected from %v", config.URL)
}

func runEmbeddedMode(config *agentconfig.Config) {
	apiURL := fmt.Sprintf("https://%s/api/connectionapps", config.URL)
	if config.IsInsecure() {
		apiURL = fmt.Sprintf("http://%s/api/connectionapps", config.URL)
	}
	dsnKey := config.Token

	connectionList, connectionEnvVal, err := getConnectionList()
	if err != nil {
		log.Error(err)
		return
	}
	vs := version.Get()
	log.Infof("version=%v, platform=%v, api-url=%v, strict-tls=%v, connections=%v - starting agent",
		vs.Version, vs.Platform, apiURL, vs.StrictTLS, connectionList)
	for {
		grpcURL := fetchGrpcURL(apiURL, dsnKey, connectionList)
		isInsecure := !vs.StrictTLS && (strings.HasPrefix(grpcURL, "http://") || strings.HasPrefix(grpcURL, "grpc://"))
		log.Infof("connecting to %v, tls=%v", grpcURL, !isInsecure)
		srvAddr, err := grpc.ParseServerAddress(grpcURL)
		if err != nil {
			log.Error("failed parsing grpc address, err=%v", err)
			continue
		}
		client, err := grpc.Connect(
			grpc.ClientConfig{
				ServerAddress: srvAddr,
				Token:         dsnKey,
				UserAgent:     defaultUserAgent,
				Insecure:      isInsecure,
			},
			grpc.WithOption("origin", pb.ConnectionOriginAgent),
			grpc.WithOption("connection-items", connectionEnvVal))
		if err != nil {
			log.Error("failed connecting to grpc gateway, err=%v", err)
			continue
		}
		agentConfig := &agentconfig.Config{
			Token:     dsnKey,
			URL:       grpcURL,
			Type:      clientconfig.ModeDsn,
			AgentMode: pb.AgentModeEmbeddedType,
		}
		err = New(client, agentConfig).Run()
		if err != io.EOF {
			log.Errorf("disconnected from %v, err=%v", grpcURL, err.Error())
			continue
		}
		log.Info("disconnected from %v", grpcURL)
	}
}

func fetchGrpcURL(apiURL, dsnKey string, connectionItems []string) string {
	log.Infof("waiting for connection request")
	reqBody, _ := json.Marshal(map[string]any{
		"connection":       "", // deprecated
		"connection_items": connectionItems,
	})
	for {
		ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
		defer cancelFn()
		resp, err := func() (*http.Response, error) {
			req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(reqBody))
			if err != nil {
				return nil, err
			}
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", dsnKey))
			return http.DefaultClient.Do(req)
		}()
		if err != nil {
			log.Warnf("failed connecting to api, err=%v", err)
			time.Sleep(time.Second * 10) // backoff
			continue
		}

		defer resp.Body.Close()
		switch resp.StatusCode {
		case 200:
			var data map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
				log.Warnf("failed decoding response, err=%v", err)
				time.Sleep(time.Second * 10) // backoff
				continue
			}
			return fmt.Sprintf("%v", data["grpc_url"])
		case 204: // noop
		case 401:
			log.Warnf("dsn is disabled or invalid, contact the administrator")
		default:
			data, _ := io.ReadAll(resp.Body)
			log.Warnf("api responded status=%v, body=%v", resp.StatusCode, string(data))
		}
		time.Sleep(time.Second * 10) // backoff
	}
}

func getConnectionList() ([]string, string, error) {
	envValue := os.Getenv("HOOP_CONNECTION")
	if envValue == "" {
		return nil, "", nil
	}
	if len(envValue) > 255 {
		return nil, "", fmt.Errorf("reached max value (255) for HOOP_CONNECTION env")
	}
	var connections []string
	for _, connectionName := range strings.Split(envValue, ",") {
		if strings.HasPrefix(connectionName, "env.") {
			envName := connectionName[4:]
			connectionName = os.Getenv(envName)
			if connectionName == "" {
				return nil, "", fmt.Errorf("environment variable %q doesn't exist", envName)
			}
		}
		connectionName = strings.TrimSpace(strings.ToLower(connectionName))
		connections = append(connections, connectionName)
	}

	return connections, envValue, nil
}
