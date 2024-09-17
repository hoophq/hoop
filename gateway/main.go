package gateway

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"

	"github.com/getsentry/sentry-go"
	"github.com/hoophq/hoop/common/envloader"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/monitoring"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/agentcontroller"
	"github.com/hoophq/hoop/gateway/api"
	apiorgs "github.com/hoophq/hoop/gateway/api/orgs"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/indexer"
	"github.com/hoophq/hoop/gateway/pgrest"
	pgorgs "github.com/hoophq/hoop/gateway/pgrest/orgs"
	pgusers "github.com/hoophq/hoop/gateway/pgrest/users"
	"github.com/hoophq/hoop/gateway/review"
	"github.com/hoophq/hoop/gateway/security/idp"
	"github.com/hoophq/hoop/gateway/transport"

	// plugins
	"github.com/hoophq/hoop/gateway/transport/connectionstatus"
	pluginsrbac "github.com/hoophq/hoop/gateway/transport/plugins/accesscontrol"
	pluginsaudit "github.com/hoophq/hoop/gateway/transport/plugins/audit"
	pluginsdlp "github.com/hoophq/hoop/gateway/transport/plugins/dlp"
	pluginsindex "github.com/hoophq/hoop/gateway/transport/plugins/index"
	pluginsreview "github.com/hoophq/hoop/gateway/transport/plugins/review"
	pluginsslack "github.com/hoophq/hoop/gateway/transport/plugins/slack"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	pluginswebhooks "github.com/hoophq/hoop/gateway/transport/plugins/webhooks"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
)

func Run() {
	ver := version.Get()
	log.Infof("version=%s, compiler=%s, go=%s, platform=%s, commit=%s, multitenant=%v, build-date=%s",
		ver.Version, ver.Compiler, ver.GoVersion, ver.Platform, ver.GitCommit, pgusers.IsOrgMultiTenant(), ver.BuildDate)

	// TODO: refactor to load all app gateway runtime configuration in this method
	if err := appconfig.Load(); err != nil {
		log.Fatalf("failed loading gateway configuration, reason=%v", err)
	}
	apiURL := appconfig.Get().ApiURL()
	if err := changeWebappApiURL(apiURL); err != nil {
		log.Fatal(err)
	}

	tlsConfig, err := loadServerCertificates()
	if err != nil {
		log.Fatal(err)
	}

	// by default start postgrest process
	if err := pgrest.Run(); err != nil {
		log.Fatal(err)
	}

	idProvider := idp.NewProvider(apiURL)
	grpcURL := os.Getenv("GRPC_URL")
	if grpcURL == "" {
		scheme := "grpcs"
		if appconfig.Get().ApiScheme() == "http" {
			scheme = "grpc"
		}
		grpcURL = fmt.Sprintf("%s://%s:8443", scheme, appconfig.Get().ApiHostname())
	}

	reviewService := review.Service{}
	if !pgusers.IsOrgMultiTenant() {
		log.Infof("provisioning default organization")
		ctx, err := pgorgs.CreateDefaultOrganization()
		if err != nil {
			log.Fatal(err)
		}
		_, _, err = apiorgs.ProvisionOrgAgentKey(ctx, grpcURL)
		if err != nil && err != apiorgs.ErrAlreadyExists {
			log.Errorf("failed provisioning org agent key, reason=%v", err)
		}
	}

	a := &api.Api{
		IndexerHandler: indexer.Handler{},
		ReviewHandler:  review.Handler{Service: &reviewService},
		IDProvider:     idProvider,
		GrpcURL:        grpcURL,
		TLSConfig:      tlsConfig,
	}

	g := &transport.Server{
		TLSConfig:     tlsConfig,
		ApiHostname:   appconfig.Get().ApiHostname(),
		ReviewService: reviewService,
		IDProvider:    idProvider,
	}
	// order matters
	plugintypes.RegisteredPlugins = []plugintypes.Plugin{
		pluginsreview.New(
			&review.Service{TransportService: g},
			idProvider.ApiURL,
		),
		pluginsaudit.New(),
		pluginsindex.New(),
		pluginsdlp.New(),
		pluginsrbac.New(),
		pluginswebhooks.New(),
		pluginsslack.New(
			&review.Service{TransportService: g},
			idProvider),
	}
	reviewService.TransportService = g

	for _, p := range plugintypes.RegisteredPlugins {
		pluginContext := plugintypes.Context{}
		if err := p.OnStartup(pluginContext); err != nil {
			log.Fatalf("failed initializing plugin %s, reason=%v", p.Name(), err)
		}
	}
	sentryStarted, _ := monitoring.StartSentry()
	if err := agentcontroller.Run(grpcURL); err != nil {
		err := fmt.Errorf("failed to start agent controller, reason=%v", err)
		log.Warn(err)
		sentry.CaptureException(err)
	}
	connectionstatus.InitConciliationProcess()
	streamclient.InitProxyMemoryCleanup()

	if grpc.ShouldDebugGrpc() {
		log.SetGrpcLogger()
	}

	log.Infof("starting servers, authmethod=%v", appconfig.Get().AuthMethod())
	go g.StartRPCServer()
	a.StartAPI(sentryStarted)
}

func changeWebappApiURL(apiURL string) error {
	if apiURL != "" {
		staticUiPath := os.Getenv("STATIC_UI_PATH")
		if staticUiPath == "" {
			staticUiPath = "/app/ui/public"
		}
		appJsFile := filepath.Join(staticUiPath, "js/app.js")
		appJsFileOrigin := filepath.Join(staticUiPath, "js/app.origin.js")
		if appBytes, err := os.ReadFile(appJsFileOrigin); err == nil {
			if err := os.WriteFile(appJsFile, appBytes, 0644); err != nil {
				return fmt.Errorf("failed saving app.js file, reason=%v", err)
			}
			log.Infof("replacing api url from origin at %v with %v", appJsFile, apiURL)
			appBytes = bytes.ReplaceAll(appBytes, []byte(`http://localhost:8009`), []byte(apiURL))
			if err := os.WriteFile(appJsFile, appBytes, 0644); err != nil {
				return fmt.Errorf("failed saving app.js file, reason=%v", err)
			}
			appBytes = bytes.ReplaceAll(appBytes, []byte(`http://localhost:4001`), []byte(apiURL))
			if err := os.WriteFile(appJsFile, appBytes, 0644); err != nil {
				return fmt.Errorf("failed saving app.js file, reason=%v", err)
			}
			return nil
		}
		appBytes, err := os.ReadFile(appJsFile)
		if err != nil {
			log.Warnf("failed opening webapp js file %v, reason=%v", appJsFile, err)
			return nil
		}
		// create a copy to allow overriding the api url
		if err := os.WriteFile(appJsFileOrigin, appBytes, 0644); err != nil {
			return fmt.Errorf("failed creating app.origin.js copy file at %v, reason=%v", appJsFileOrigin, err)
		}

		log.Infof("replacing api url at %v with %v", appJsFile, apiURL)
		appBytes = bytes.ReplaceAll(appBytes, []byte(`http://localhost:8009`), []byte(apiURL))
		if err := os.WriteFile(appJsFile, appBytes, 0644); err != nil {
			return fmt.Errorf("failed saving app.js file, reason=%v", err)
		}
		appBytes = bytes.ReplaceAll(appBytes, []byte(`http://localhost:4001`), []byte(apiURL))
		if err := os.WriteFile(appJsFile, appBytes, 0644); err != nil {
			return fmt.Errorf("failed saving app.js file, reason=%v", err)
		}
	}
	return nil
}

func loadServerCertificates() (*tls.Config, error) {
	tlsCA, err := envloader.GetEnv("TLS_CA")
	if err != nil {
		return nil, fmt.Errorf("faile loading TLS_CA: %v", err)
	}
	tlsKey, err := envloader.GetEnv("TLS_KEY")
	if err != nil {
		return nil, fmt.Errorf("faile loading TLS_KEY: %v", err)
	}
	tlsCert, err := envloader.GetEnv("TLS_CERT")
	if err != nil {
		return nil, fmt.Errorf("faile loading TLS_CERT: %v", err)
	}
	if tlsKey == "" || tlsCert == "" {
		return nil, nil
	}
	cert, err := tls.X509KeyPair([]byte(tlsCert), []byte(tlsKey))
	if err != nil {
		return nil, fmt.Errorf("failed parsing key pair, err=%v", err)
	}
	var certPool *x509.CertPool
	if tlsCA != "" {
		certPool = x509.NewCertPool()
		if !certPool.AppendCertsFromPEM([]byte(tlsCA)) {
			return nil, fmt.Errorf("failed creating cert pool for TLS_CA")
		}
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      certPool,
	}, nil
}
