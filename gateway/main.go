package gateway

import (
	"context"
	"fmt"
	"os"

	"github.com/getsentry/sentry-go"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/log/bootstrap"
	"github.com/hoophq/hoop/common/monitoring"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/analytics"

	"github.com/hoophq/hoop/gateway/agentcontroller"
	"github.com/hoophq/hoop/gateway/api"
	"github.com/hoophq/hoop/gateway/services"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	apiorgs "github.com/hoophq/hoop/gateway/api/orgs"
	apiserverconfig "github.com/hoophq/hoop/gateway/api/serverconfig"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/externaljwt"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
	"github.com/hoophq/hoop/gateway/proxyproto/httpproxy"
	"github.com/hoophq/hoop/gateway/proxyproto/postgresproxy"
	"github.com/hoophq/hoop/gateway/proxyproto/sshproxy"
	"github.com/hoophq/hoop/gateway/rdp"
	"github.com/hoophq/hoop/gateway/transport"
	"github.com/hoophq/hoop/gateway/webappjs"

	// plugins
	"github.com/hoophq/hoop/gateway/transport/connectionstatus"
	pluginsrbac "github.com/hoophq/hoop/gateway/transport/plugins/accesscontrol"
	pluginsaudit "github.com/hoophq/hoop/gateway/transport/plugins/audit"
	pluginsdlp "github.com/hoophq/hoop/gateway/transport/plugins/dlp"
	pluginsreview "github.com/hoophq/hoop/gateway/transport/plugins/review"
	pluginsslack "github.com/hoophq/hoop/gateway/transport/plugins/slack"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
	pluginswebhooks "github.com/hoophq/hoop/gateway/transport/plugins/webhooks"
	"github.com/hoophq/hoop/gateway/transport/streamclient"
)

func Run() {
	bootstrap.Start()
	ver := version.Get()
	bootstrap.Header(ver.Version, ver.Platform, ver.GitCommit)

	if err := appconfig.Load(); err != nil {
		log.Fatalf("failed loading gateway configuration, reason=%v", err)
	}
	if err := webappjs.ConfigureServerURL(); err != nil {
		log.Warnf("failed configuring webappjs server URL, running gateway without it, reason=%v", err)
	}

	tlsConfig, err := appconfig.Get().GetTLSConfig()
	if err != nil {
		log.Fatal(err)
	}

	bootstrap.Phase("Bootstrapping")

	pgURI, migrationPathFiles := appconfig.Get().PgURI(), appconfig.Get().MigrationPathFiles()
	migrateStep := bootstrap.Step("Database migrations")
	if err := modelsbootstrap.MigrateDB(pgURI, migrationPathFiles); err != nil {
		migrateStep.Fail(err)
		log.Fatal(err)
	}
	migrateStep.OK("")

	apiURL := appconfig.Get().FullApiURL()
	if err := models.InitDatabaseConnection(); err != nil {
		log.Fatal(err)
	}

	goMigrateStep := bootstrap.Step("Golang migrations")
	if err := modelsbootstrap.RunGolangMigrations(); err != nil {
		goMigrateStep.Fail(err)
		log.Fatalf("failed running golang migrations, reason=%v", err)
	}
	goMigrateStep.OK("")

	services.WarmFeatureFlagCache()

	if err := externaljwt.Init(context.Background()); err != nil {
		// Bootstrap failures are typically transient (bundle URL
		// unreachable, JWKS file not yet present) and the provider's
		// background refresh loop will retry. Warn instead of crashing
		// so a SPIFFE issue does not take DSN-token agents offline as
		// collateral damage. JWT-SVID auth keeps failing until the
		// bundle refreshes, which is visible via agent logs and the
		// "spiffe: bundle refresh failed" warnings.
		log.Warnf("failed initializing SPIFFE provider, background refresh will retry: %v", err)
	}

	if enabled, err := analytics.IsAnalyticsEnabled(); err == nil {
		appconfig.GetRef().SetAnalyticsTracking(enabled)
	}

	isOrgMultiTenant := appconfig.Get().OrgMultitenant()
	if !isOrgMultiTenant {
		orgStep := bootstrap.Step("Default organization")
		_, serverConfig, err := idp.NewTokenVerifierProvider()
		if err != nil {
			orgStep.Fail(err)
			log.Fatalf("failed initializing token verifier provider, reason=%v", err)
		}

		org, isNewOrg, err := models.CreateOrgGetOrganization(proto.DefaultOrgName, nil)
		if err != nil {
			orgStep.Fail(err)
			log.Fatal(err)
		}

		if isNewOrg {
			trackClient := analytics.New()
			defer trackClient.Close()
			trackClient.TrackEvent(analytics.EventDefaultOrgCreated, map[string]interface{}{"org-id": org.ID})
		}

		_, err = models.CreateDefaultRunbookConfiguration(models.DB, org.ID)
		if err != nil {
			log.Errorf("failed creating default runbook configuration, reason=%v", err)
		}

		_, _, err = apiorgs.ProvisionOrgAgentKey(org.ID, serverConfig.GrpcURL)
		if err != nil && err != apiorgs.ErrAlreadyExists {
			log.Errorf("failed provisioning org agent key, reason=%v", err)
		}

		err = models.UpsertBatchConnectionTags(apiconnections.DefaultConnectionTags(org.ID))
		if err != nil {
			log.Warnf("failed provisioning default system tags, reason=%v", err)
		}

		// TODO(san): refactor to propagate the defined user name roles as context to routes
		if err := apiserverconfig.SetGlobalGatewayUserRoles(); err != nil {
			orgStep.Fail(err)
			log.Fatalf("failed setting global gateway user roles, reason=%v", err)
		}

		orgStep.OK(fmt.Sprintf("dlp=%s", appconfig.Get().DlpProvider()))
	}

	if err := modelsbootstrap.AddDefaultRunbooks(); err != nil {
		log.Infof("failed adding default runbooks, reason=%v", err)
	}

	g := &transport.Server{
		TLSConfig:   tlsConfig,
		ApiHostname: appconfig.Get().ApiHostname(),
		AppConfig:   appconfig.Get(),
	}
	a := &api.Api{
		ReleaseConnectionFn: g.ReleaseConnectionOnReview,
		TLSConfig:           tlsConfig,
	}
	// order matters
	plugintypes.RegisteredPlugins = []plugintypes.Plugin{
		pluginsreview.New(apiURL),
		pluginsaudit.New(),
		pluginsdlp.New(),
		pluginsrbac.New(),
		pluginswebhooks.New(),
		pluginsslack.New(g.ReleaseConnectionOnReview),
	}

	for _, p := range plugintypes.RegisteredPlugins {
		pluginContext := plugintypes.Context{}
		if err := p.OnStartup(pluginContext); err != nil {
			log.Fatalf("failed initializing plugin %s, reason=%v", p.Name(), err)
		}
	}

	_, _ = monitoring.StartSentry(appconfig.Get().ApiHostname())
	if isOrgMultiTenant {
		// grpc url from env is used for multi tenant setups
		if err := agentcontroller.Run(os.Getenv("GRPC_URL")); err != nil {
			err := fmt.Errorf("failed to start agent controller, reason=%v", err)
			log.Warn(err)
			sentry.CaptureException(err)
		}
	}

	connectionstatus.InitConciliationProcess()
	streamclient.InitProxyMemoryCleanup()

	if grpc.ShouldDebugGrpc() {
		log.SetGrpcLogger()
	}

	serverConfig, err := models.GetServerMiscConfig()
	if err != nil && err != models.ErrNotFound {
		log.Fatalf("failed to get server config, reason=%v", err)
	}

	bootstrap.Phase("Starting proxies")
	if serverConfig != nil {
		pgc := serverConfig.PostgresServerConfig
		if pgc != nil && pgc.ListenAddress != "" {
			step := bootstrap.Step("Postgres proxy")
			err := postgresproxy.GetServerInstance().Start(serverConfig.PostgresServerConfig.ListenAddress, tlsConfig)
			if err != nil {
				step.Fail(err)
				log.Fatalf("failed to start postgres server, reason=%v", err)
			}
			step.OK(pgc.ListenAddress)
		}

		sshc := serverConfig.SSHServerConfig
		if sshc != nil && sshc.ListenAddress != "" && len(sshc.HostsKey) > 0 {
			step := bootstrap.Step("SSH proxy")
			err := sshproxy.GetServerInstance().Start(
				serverConfig.SSHServerConfig.ListenAddress,
				serverConfig.SSHServerConfig.HostsKey,
			)
			if err != nil {
				step.Fail(err)
				log.Fatalf("failed to start ssh server, reason=%v", err)
			}
			step.OK(sshc.ListenAddress)
		}

		rdpc := serverConfig.RDPServerConfig
		if rdpc != nil && rdpc.ListenAddress != "" {
			step := bootstrap.Step("RDP proxy")
			// Initialize RDP bitmap parser for session recording
			if err := rdp.InitParser(); err != nil {
				log.Warnf("failed to initialize RDP bitmap parser, recording will store raw data: %v", err)
			}
			err = rdp.GetServerInstance().Start(
				serverConfig.RDPServerConfig.ListenAddress, tlsConfig, appconfig.Get().GatewayAllowPlaintext(),
			)
			if err != nil {
				step.Fail(err)
				log.Fatalf("failed to start rdp server, reason=%v", err)
			}
			step.OK(rdpc.ListenAddress)
		}

		httpc := serverConfig.HttpProxyServerConfig
		if httpc != nil && httpc.ListenAddress != "" {
			step := bootstrap.Step("HTTP proxy")
			err = httpproxy.GetServerInstance().Start(httpc.ListenAddress, tlsConfig)
			if err != nil {
				step.Fail(err)
				log.Fatalf("failed to start http proxy server, reason=%v", err)
			}
			tlsState := "plain"
			if tlsConfig != nil {
				tlsState = "tls"
			}
			step.OK(fmt.Sprintf("%s %s", httpc.ListenAddress, tlsState))
		}
	}

	bootstrap.Phase("Starting API")
	grpcStep := bootstrap.Step("gRPC gateway")
	go g.StartRPCServer()
	tlsState := "tls off"
	if tlsConfig != nil {
		tlsState = "tls on"
	}
	grpcStep.OK(fmt.Sprintf(":8010 %s", tlsState))

	apiStep := bootstrap.Step("HTTP API")
	authDetail := fmt.Sprintf("auth=%s", appconfig.Get().AuthMethod())
	if len(appconfig.Get().ApiKey()) > 0 {
		authDetail += " api-key=set"
	}
	apiStep.OK(fmt.Sprintf("%s %s", appconfig.Get().ApiURL(), authDetail))

	urls := map[string]string{
		"Web UI":  appconfig.Get().ApiURL(),
		"Gateway": "localhost:8010",
	}
	bootstrap.Ready(urls)

	a.StartAPI()
}
