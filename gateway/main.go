package gateway

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/getsentry/sentry-go"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/monitoring"
	"github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/agentcontroller"
	"github.com/hoophq/hoop/gateway/api"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	apiorgs "github.com/hoophq/hoop/gateway/api/orgs"
	apiserverconfig "github.com/hoophq/hoop/gateway/api/serverconfig"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
	modelsbootstrap "github.com/hoophq/hoop/gateway/models/bootstrap"
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
	ver := version.Get()
	log.Infof("version=%s, compiler=%s, go=%s, platform=%s, commit=%s, multitenant=%v, build-date=%s",
		ver.Version, ver.Compiler, ver.GoVersion, ver.Platform, ver.GitCommit, appconfig.Get().OrgMultitenant(), ver.BuildDate)

	if err := appconfig.Load(); err != nil {
		log.Fatalf("failed loading gateway configuration, reason=%v", err)
	}
	if err := webappjs.ConfigureServerURL(); err != nil {
		log.Fatal(err)
	}

	tlsConfig, err := loadServerCertificates()
	if err != nil {
		log.Fatal(err)
	}

	pgURI, migrationPathFiles := appconfig.Get().PgURI(), appconfig.Get().MigrationPathFiles()
	if err := modelsbootstrap.MigrateDB(pgURI, migrationPathFiles); err != nil {
		log.Fatal(err)
	}

	apiURL := appconfig.Get().FullApiURL()
	if err := models.InitDatabaseConnection(); err != nil {
		log.Fatal(err)
	}

	isOrgMultiTenant := appconfig.Get().OrgMultitenant()
	if !isOrgMultiTenant {
		log.Infof("provisioning default organization")
		_, serverConfig, err := idp.NewTokenVerifierProvider()
		if err != nil {
			log.Fatalf("failed initializing token verifier provider, reason=%v", err)
		}

		org, err := models.CreateOrgGetOrganization(proto.DefaultOrgName, nil)
		if err != nil {
			log.Fatal(err)
		}

		_, _, err = apiorgs.ProvisionOrgAgentKey(org.ID, serverConfig.GrpcURL)
		if err != nil && err != apiorgs.ErrAlreadyExists {
			log.Errorf("failed provisioning org agent key, reason=%v", err)
		}

		err = models.UpsertBatchConnectionTags(apiconnections.DefaultConnectionTags(org.ID))
		if err != nil {
			log.Warnf("failed provisioning default system tags, reason=%v", err)
		}

		var migrationErr error
		if appconfig.Get().DlpProvider() == "mspresidio" {
			migrationErr = models.MigratePluginConnectionToDataMaskingRules(org.ID)
			if migrationErr != nil {
				log.Warnf("failed migrating plugin connections to data masking rules, reason=%v", migrationErr)
			}
		}

		// TODO(san): refactor to propagate the defined user name roles as context to routes
		if err := apiserverconfig.SetGlobalGatewayUserRoles(); err != nil {
			log.Fatalf("failed setting global gateway user roles, reason=%v", err)
		}

		log.Infof("self hosted setup completed, dlp-provider=%s, plugin-connection-migration-err=%v",
			appconfig.Get().DlpProvider(), migrationErr)
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
	sentryStarted, _ := monitoring.StartSentry()
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

	log.Infof("starting servers, env-authmethod=%v, env-api-key-set=%v",
		appconfig.Get().AuthMethod(), len(appconfig.Get().ApiKey()) > 0)
	go g.StartRPCServer()
	a.StartAPI(sentryStarted)
}

func loadServerCertificates() (*tls.Config, error) {
	conf := appconfig.Get()
	tlsCA, tlsKey, tlsCert := conf.GatewayTLSCa(), conf.GatewayTLSKey(), conf.GatewayTLSCert()
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
