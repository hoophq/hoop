package gateway

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/runopsio/hoop/common/clientconfig"
	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/monitoring"
	"github.com/runopsio/hoop/common/version"
	"github.com/runopsio/hoop/gateway/agent"
	"github.com/runopsio/hoop/gateway/analytics"
	"github.com/runopsio/hoop/gateway/api"
	"github.com/runopsio/hoop/gateway/connection"
	"github.com/runopsio/hoop/gateway/indexer"
	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/runbooks"
	"github.com/runopsio/hoop/gateway/security"
	"github.com/runopsio/hoop/gateway/security/idp"
	"github.com/runopsio/hoop/gateway/session"
	xtdb "github.com/runopsio/hoop/gateway/storage"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/transport"
	"github.com/runopsio/hoop/gateway/user"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	// plugins
	"github.com/runopsio/hoop/gateway/transport/adminapi"
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

func Run(listenAdmAddr string) {
	ver := version.Get()
	log.Infof("version=%s, compiler=%s, go=%s, platform=%s, commit=%s, multitenant=%v, build-date=%s",
		ver.Version, ver.Compiler, ver.GoVersion, ver.Platform, ver.GitCommit, user.IsOrgMultiTenant(), ver.BuildDate)

	apiURL := os.Getenv("API_URL")
	if err := changeWebappApiURL(apiURL); err != nil {
		log.Fatal(err)
	}

	// by default start postgrest process
	pgrestUrl, pgrestJwtSecret, err := pgRestConfig()
	if err != nil {
		log.Fatal(err)
	}
	pgrest.JwtSecretKey = pgrestJwtSecret
	pgrest.URL = pgrestUrl
	if err := startPostgrestProcessManager(pgrestJwtSecret); err != nil {
		log.Fatal(err)
	}

	s := xtdb.New()
	// sync xtdb if it's a legacy environment
	if !pgrest.Rollout {
		defer log.Sync()
		log.Infof("syncing xtdb at %s", s.Address())
		if err := s.Sync(time.Second * 80); err != nil {
			log.Fatal(err)
		}
		log.Infof("sync with success")
	}

	storev2 := storagev2.NewStorage(nil)

	profile := os.Getenv("PROFILE")
	idProvider := idp.NewProvider(profile)
	analyticsService := analytics.New()

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

	agentService := agent.Service{Storage: &agent.Storage{Storage: s}}
	connectionService := connection.Service{Storage: &connection.Storage{Storage: s}}
	userService := user.Service{Storage: &user.Storage{Storage: s}}
	sessionService := session.Service{Storage: &session.Storage{Storage: s}}
	reviewService := review.Service{Storage: &review.Storage{Storage: s}}
	notificationService := getNotification()
	securityService := security.Service{
		Storage:     &security.Storage{Storage: s},
		Provider:    idProvider,
		UserService: &userService,
		Analytics:   analyticsService}

	if !user.IsOrgMultiTenant() {
		log.Infof("provisioning / promoting default organization")
		if err := userService.CreateDefaultOrganization(); err != nil {
			log.Fatal(err)
		}
	}

	a := &api.Api{
		AgentHandler:      agent.Handler{Service: &agentService},
		ConnectionHandler: connection.Handler{Service: &connectionService},
		UserHandler:       user.Handler{Service: &userService, Analytics: analyticsService},
		SessionHandler:    session.Handler{ApiURL: apiURL, Service: &sessionService, ConnectionService: &connectionService},
		IndexerHandler:    indexer.Handler{},
		ReviewHandler:     review.Handler{Service: &reviewService},
		SecurityHandler:   security.Handler{Service: &securityService},
		RunbooksHandler:   runbooks.Handler{ConnectionService: &connectionService},
		IDProvider:        idProvider,
		Profile:           profile,
		Analytics:         analyticsService,
		GrpcURL:           grpcURL,

		StoreV2: storev2,
	}

	g := &transport.Server{
		AgentService:         agentService,
		ConnectionService:    connectionService,
		UserService:          userService,
		SessionService:       sessionService,
		ReviewService:        reviewService,
		NotificationService:  notificationService,
		IDProvider:           idProvider,
		Profile:              profile,
		GcpDLPRawCredentials: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS_JSON"),
		PluginRegistryURL:    os.Getenv("PLUGIN_REGISTRY_URL"),
		PyroscopeIngestURL:   os.Getenv("PYROSCOPE_INGEST_URL"),
		PyroscopeAuthToken:   os.Getenv("PYROSCOPE_AUTH_TOKEN"),
		AgentSentryDSN:       os.Getenv("AGENT_SENTRY_DSN"),
		Analytics:            analyticsService,

		StoreV2: storev2,
	}
	// order matters
	g.RegisteredPlugins = []plugintypes.Plugin{
		pluginsreview.New(
			&review.Service{Storage: &review.Storage{Storage: s}, TransportService: g},
			&user.Service{Storage: &user.Storage{Storage: s}},
			notificationService,
			idProvider.ApiURL,
		),
		pluginsaudit.New(),
		pluginsindex.New(&session.Storage{Storage: s}),
		pluginsdlp.New(),
		pluginsrbac.New(),
		pluginswebhooks.New(&review.Service{Storage: &review.Storage{Storage: s}, TransportService: g}),
		pluginsslack.New(
			&review.Service{Storage: &review.Storage{Storage: s}, TransportService: g},
			&user.Service{Storage: &user.Storage{Storage: s}},
			idProvider),
		pluginsdcm.New(),
	}
	plugintypes.RegisteredPlugins = g.RegisteredPlugins

	for _, p := range g.RegisteredPlugins {
		pluginContext := plugintypes.Context{}
		if err := p.OnStartup(pluginContext); err != nil {
			log.Fatalf("failed initializing plugin %s, reason=%v", p.Name(), err)
		}
	}

	if g.PyroscopeIngestURL != "" && g.PyroscopeAuthToken != "" {
		log.Infof("starting profiler, ingest-url=%v", g.PyroscopeIngestURL)
		_, err := monitoring.StartProfiler("gateway", monitoring.ProfilerConfig{
			PyroscopeServerAddress: g.PyroscopeIngestURL,
			PyroscopeAuthToken:     g.PyroscopeAuthToken,
			Environment:            g.IDProvider.ApiURL,
		})
		if err != nil {
			log.Fatalf("failed starting profiler, err=%v", err)
		}
	}
	sentryStarted, err := monitoring.StartSentry(nil, monitoring.SentryConfig{
		DSN:         os.Getenv("SENTRY_DSN"),
		Environment: g.IDProvider.ApiURL,
	})
	if err != nil {
		log.Fatalf("failed starting sentry, err=%v", err)
	}
	reviewService.TransportService = g

	if grpc.ShouldDebugGrpc() {
		log.SetGrpcLogger()
	}

	log.Infof("profile=%v - starting servers", profile)
	go g.StartRPCServer()
	go adminapi.RunServer(listenAdmAddr)
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
	if os.Getenv("NOTIFICATIONS_BRIDGE_CONFIG") != "" && os.Getenv("ORG_MULTI_TENANT") != "true" {
		mBridgeConfigRaw := []byte(os.Getenv("NOTIFICATIONS_BRIDGE_CONFIG"))
		var mBridgeConfigMap map[string]string
		if err := json.Unmarshal(mBridgeConfigRaw, &mBridgeConfigMap); err != nil {
			log.Fatalf("failed decoding notifications bridge config")
		}
		log.Printf("Bridge notifications selected")
		matterbridgeConfig := `[slack]
[slack.myslack]
Token="%s"
PreserveThreading=true

[api.myapi]
BindAddress="127.0.0.1:4242"
Buffer=10000

[[gateway]]
name="hoop-notifications-bridge"
enable=true

[[gateway.in]]
account="api.myapi"
channel="api"

[[gateway.out]]
account="slack.myslack"
channel="general"`
		matterbridgeFolder, err := clientconfig.NewHomeDir("matterbridge")
		if err != nil {
			log.Fatal(err)
		}
		configFile := filepath.Join(matterbridgeFolder, "matterbridge.toml")
		configFileBytes := []byte(fmt.Sprintf(matterbridgeConfig, mBridgeConfigMap["slackBotToken"]))
		err = os.WriteFile(configFile, configFileBytes, 0600)
		if err != nil {
			log.Fatal(err)
		}

		err = exec.Command("matterbridge", "-conf", configFile).Start()

		if err != nil {
			log.Fatal(err)
		}
		return notification.NewMatterbridge()
	} else if os.Getenv("SMTP_HOST") != "" {
		log.Printf("SMTP notifications selected")
		return notification.NewSmtpSender()
	}
	log.Printf("MagicBell notifications selected")
	return notification.NewMagicBell()
}

func startPostgrestProcessManager(pgrestJwtSecret []byte) error {
	postgrestBinFile := "/usr/local/bin/postgrest"
	if _, err := os.Stat(postgrestBinFile); err != nil && os.IsNotExist(err) {
		return nil
	}
	// validate if the migration files are present
	if _, err := os.Stat("/app/migrations/000001_init.up.sql"); err != nil {
		return fmt.Errorf("failed validating migration files, err=%v", err)
	}

	// migration
	dbURL := fmt.Sprintf("%s?sslmode=disable", toPostgresURI())
	m, err := migrate.New("file:///app/migrations", dbURL)
	if err != nil {
		return fmt.Errorf("failed initializing db migration, err=%v", err)
	}
	ver, dirty, err := m.Version()
	if err != nil && err != migrate.ErrNilVersion {
		return fmt.Errorf("failed obtaining db migration version, err=%v", err)
	}
	if dirty {
		return fmt.Errorf("database is in a dirty state, requires manual intervention to fix it")
	}
	log.Infof("loaded migration version=%v, is-nil-version=%v", ver, err == migrate.ErrNilVersion)
	err = m.Up()
	if err != nil && err != migrate.ErrNilVersion && err != migrate.ErrNoChange {
		return fmt.Errorf("failed running db migration, err=%v", err)
	}
	log.Infof("processed db migration with success, nochange=%v", err == migrate.ErrNoChange)

	// https://postgrest.org/en/stable/references/configuration.html#env-variables-config
	envs := []string{
		"PGRST_DB_ANON_ROLE=web_anon",
		"PGRST_DB_CHANNEL_ENABLED=False",
		"PGRST_DB_CONFIG=False",
		"PGRST_DB_PLAN_ENABLED=True",
		"PGRST_LOG_LEVEL=warn",
		"PGRST_SERVER_HOST=!4",
		"PGRST_SERVER_PORT=8008",
		fmt.Sprintf("PGRST_DB_URI=%s", toPostgresURI()),
		fmt.Sprintf("PGRST_JWT_SECRET=%s", string(pgrestJwtSecret)),
	}

	startProcessFn := func(i int) {
		log.Infof("starting postgrest process, attempt=%v ...", i)
		cmd := exec.Command(postgrestBinFile)
		cmd.Env = envs
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Errorf("failed running postgrest process, err=%v", err)
			return
		}
		pid := -1
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}
		log.Infof("postgrest process (pid:%v) finished", pid)
	}

	go func() {
		for i := 1; ; i++ {
			startProcessFn(i)
			// give some time to retry
			time.Sleep(time.Second * 5)
		}
	}()

	for i := 1; ; i++ {
		if i > 15 {
			log.Fatal("max attempts (15) reached. failed to validate postgrest liveness at %v", pgrest.URL.Host)
		}
		if err := checkAddrLiveness(pgrest.URL.Host); err != nil {
			time.Sleep(time.Second * 1)
			continue
		}
		log.Infof("postgrest is ready at %v", pgrest.URL.Host)
		break
	}
	return nil
}

func toPostgresURI() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s",
		os.Getenv("PG_USER"),
		os.Getenv("PG_PASSWORD"),
		os.Getenv("PG_HOST"),
		os.Getenv("PG_PORT"),
		os.Getenv("PG_DB"),
	)
}

func pgRestConfig() (u *url.URL, jwtSecret []byte, err error) {
	secretRandomBytes := make([]byte, 32)
	if _, err := rand.Read(secretRandomBytes); err != nil {
		return nil, nil, fmt.Errorf("failed generating entropy, err=%v", err)
	}

	pgrestUrlStr := os.Getenv("PGREST_URL")
	if pgrestUrlStr == "" {
		pgrestUrlStr = "http://127.0.0.1:8008"
	}
	pgrestUrl, err := url.Parse(pgrestUrlStr)
	if err != nil {
		return nil, nil, fmt.Errorf("PGREST_URL in wrong format, err=%v", err)
	}
	return pgrestUrl, []byte(base64.RawURLEncoding.EncodeToString(secretRandomBytes)), nil
}

func checkAddrLiveness(addr string) error {
	timeout := time.Second * 3
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return fmt.Errorf("not responding, err=%v", err)
	}
	_ = conn.Close()
	return nil
}
