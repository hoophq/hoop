package gateway

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/monitoring"
	"github.com/runopsio/hoop/common/version"
	"github.com/runopsio/hoop/gateway/agentcontroller"
	"github.com/runopsio/hoop/gateway/api"
	"github.com/runopsio/hoop/gateway/indexer"
	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/runbooks"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/transport"
	"github.com/runopsio/hoop/gateway/user"

	// plugins
	pluginsrbac "github.com/runopsio/hoop/gateway/transport/plugins/accesscontrol"
	pluginsaudit "github.com/runopsio/hoop/gateway/transport/plugins/audit"
	pluginsdcm "github.com/runopsio/hoop/gateway/transport/plugins/dcm"
	pluginsdlp "github.com/runopsio/hoop/gateway/transport/plugins/dlp"
	pluginsindex "github.com/runopsio/hoop/gateway/transport/plugins/index"
	pluginsreview "github.com/runopsio/hoop/gateway/transport/plugins/review"
	pluginsslack "github.com/runopsio/hoop/gateway/transport/plugins/slack"
	plugintypes "github.com/runopsio/hoop/gateway/transport/plugins/types"
	pluginswebhooks "github.com/runopsio/hoop/gateway/transport/plugins/webhooks"
)

func Run() {
	ver := version.Get()
	log.Infof("version=%s, compiler=%s, go=%s, platform=%s, commit=%s, multitenant=%v, build-date=%s",
		ver.Version, ver.Compiler, ver.GoVersion, ver.Platform, ver.GitCommit, user.IsOrgMultiTenant(), ver.BuildDate)

	apiURL := os.Getenv("API_URL")
	if err := changeWebappApiURL(apiURL); err != nil {
		log.Fatal(err)
	}

	// by default start postgrest process
	if err := pgrest.Run(); err != nil {
		log.Fatal(err)
	}

	storev2 := storagev2.NewStorage(nil)
	idProvider := idp.NewProvider()

	grpcURL := os.Getenv("GRPC_URL")
	if grpcURL == "" {
		u, err := url.Parse(idProvider.ApiURL)
		if err != nil {
			log.Fatalf("failed parsing API_URL, reason=%v", err)
		}
		scheme := "grpcs"
		if u.Scheme == "http" {
			scheme = "grpc"
		}
		grpcURL = fmt.Sprintf("%s://%s:8443", scheme, u.Hostname())
	}

	userService := user.Service{Storage: &user.Storage{}}
	reviewService := review.Service{Storage: &review.Storage{}}
	notificationService := getNotification()

	if !user.IsOrgMultiTenant() {
		log.Infof("provisioning / promoting default organization")
		if err := userService.CreateDefaultOrganization(); err != nil {
			log.Fatal(err)
		}
	}

	a := &api.Api{
		IndexerHandler:  indexer.Handler{},
		ReviewHandler:   review.Handler{Service: &reviewService},
		RunbooksHandler: runbooks.Handler{},
		IDProvider:      idProvider,
		GrpcURL:         grpcURL,

		StoreV2: storev2,
	}

	g := &transport.Server{
		ReviewService:        reviewService,
		NotificationService:  notificationService,
		IDProvider:           idProvider,
		GcpDLPRawCredentials: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON"),
		PluginRegistryURL:    os.Getenv("PLUGIN_REGISTRY_URL"),
		PyroscopeIngestURL:   os.Getenv("PYROSCOPE_INGEST_URL"),
		PyroscopeAuthToken:   os.Getenv("PYROSCOPE_AUTH_TOKEN"),
		AgentSentryDSN:       "https://a6ecaeba31684f02ab8606a59301cd15@o4504559799566336.ingest.sentry.io/4504571759230976",

		StoreV2: storev2,
	}
	// order matters
	g.RegisteredPlugins = []plugintypes.Plugin{
		pluginsreview.New(
			&review.Service{Storage: &review.Storage{}, TransportService: g},
			&user.Service{Storage: &user.Storage{}},
			notificationService,
			idProvider.ApiURL,
		),
		pluginsaudit.New(),
		pluginsindex.New(),
		pluginsdlp.New(),
		pluginsrbac.New(),
		pluginswebhooks.New(&review.Service{Storage: &review.Storage{}, TransportService: g}),
		pluginsslack.New(
			&review.Service{Storage: &review.Storage{}, TransportService: g},
			&user.Service{Storage: &user.Storage{}},
			idProvider),
		pluginsdcm.New(),
	}
	plugintypes.RegisteredPlugins = g.RegisteredPlugins
	reviewService.TransportService = g

	for _, p := range g.RegisteredPlugins {
		pluginContext := plugintypes.Context{}
		if err := p.OnStartup(pluginContext); err != nil {
			log.Fatalf("failed initializing plugin %s, reason=%v", p.Name(), err)
		}
	}
	sentryStarted, err := monitoring.StartSentry(nil, monitoring.SentryConfig{
		DSN:         "https://7c3bcdf7772943b9b70bcf69b07408ae@o4504559799566336.ingest.sentry.io/4504559805923328",
		Environment: g.IDProvider.ApiURL,
	})
	if err != nil {
		log.Fatalf("failed starting sentry, err=%v", err)
	}

	if err := agentcontroller.Run(grpcURL); err != nil {
		err := fmt.Errorf("failed to start agent controller, reason=%v", err)
		log.Warn(err)
		sentry.CaptureException(err)
	}

	if grpc.ShouldDebugGrpc() {
		log.SetGrpcLogger()
	}

	log.Infof("starting servers")
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
		appBytes, err := os.ReadFile(appJsFile)
		if err != nil {
			log.Warnf("failed opening webapp js file %v, reason=%v", appJsFile, err)
			return nil
		}
		log.Infof("replacing api url from %v with %v", appJsFile, apiURL)
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

func getNotification() notification.Service {
	if os.Getenv("SMTP_HOST") != "" {
		log.Infof("SMTP notifications selected")
		return notification.NewSmtpSender()
	}
	log.Infof("MagicBell notifications selected")
	return notification.NewMagicBell()
}
