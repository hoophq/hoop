package api

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-contrib/static"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/proxyproto/ssmproxy"
	"github.com/hoophq/hoop/gateway/rdp"
	"go.uber.org/zap"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/analytics"
	accessrequestsapi "github.com/hoophq/hoop/gateway/api/accessrequests"
	apiagents "github.com/hoophq/hoop/gateway/api/agents"
	apiai "github.com/hoophq/hoop/gateway/api/ai"
	apikeys "github.com/hoophq/hoop/gateway/api/apikeys"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	apiattributes "github.com/hoophq/hoop/gateway/api/attributes"
	auditlogapi "github.com/hoophq/hoop/gateway/api/auditlog"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	apidatamasking "github.com/hoophq/hoop/gateway/api/datamasking"
	apifeatures "github.com/hoophq/hoop/gateway/api/features"
	apiguardrails "github.com/hoophq/hoop/gateway/api/guardrails"
	apihealthz "github.com/hoophq/hoop/gateway/api/healthz"
	apijiraintegration "github.com/hoophq/hoop/gateway/api/integrations"
	awsintegration "github.com/hoophq/hoop/gateway/api/integrations/aws"
	loginlocalapi "github.com/hoophq/hoop/gateway/api/login/local"
	loginoidcapi "github.com/hoophq/hoop/gateway/api/login/oidc"
	loginsamlapi "github.com/hoophq/hoop/gateway/api/login/saml"
	metricsapi "github.com/hoophq/hoop/gateway/api/metrics"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apiorgs "github.com/hoophq/hoop/gateway/api/orgs"
	apipluginconnections "github.com/hoophq/hoop/gateway/api/pluginconnections"
	apiplugins "github.com/hoophq/hoop/gateway/api/plugins"
	apiproxymanager "github.com/hoophq/hoop/gateway/api/proxymanager"
	apipublicserverinfo "github.com/hoophq/hoop/gateway/api/publicserverinfo"
	apireports "github.com/hoophq/hoop/gateway/api/reports"
	resourcesapi "github.com/hoophq/hoop/gateway/api/resources"
	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	apirunbooks "github.com/hoophq/hoop/gateway/api/runbooks"
	searchapi "github.com/hoophq/hoop/gateway/api/search"
	apiserverconfig "github.com/hoophq/hoop/gateway/api/serverconfig"
	apiserverinfo "github.com/hoophq/hoop/gateway/api/serverinfo"
	apimcpserver "github.com/hoophq/hoop/gateway/api/mcpserver"
	serviceaccountapi "github.com/hoophq/hoop/gateway/api/serviceaccount"
	sessionapi "github.com/hoophq/hoop/gateway/api/session"
	signupapi "github.com/hoophq/hoop/gateway/api/signup"
	userapi "github.com/hoophq/hoop/gateway/api/user"
	webhooksapi "github.com/hoophq/hoop/gateway/api/webhooks"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/transport"
)

type Api struct {
	ReleaseConnectionFn reviewapi.TransportReleaseConnectionFunc
	TLSConfig           *tls.Config
	logger              *zap.Logger
}

//	@title			Hoop Api
//	@version		1.0
//	@description	Hoop.dev is an access gateway for databases and servers with an API for packet manipulation
//	@termsOfService	https://hoop.dev/docs/legal/tos
//	@schemes		https

//	@contact.name	Help
//	@contact.url	https://help.hoop.dev
//	@contact.email	help@hoop.dev

//	@license.name	MIT
//	@license.url	https://opensource.org/license/mit

//	@tag.name	Authentication
//	@tag.description.markdown

//	@tag.name	User Management
//	@tag.description.markdown

//	@tag.name	Server Management
//	@tag.description.markdown

//	@tag.name	Features
//	@tag.description.markdown

//	@tag.name	Proxy Manager
//	@tag.description.markdown

//	@tag.name	Connections

//	@tag.name	Agents

//	@tag.name	Runbooks

//	@tag.name	Guard Rails

//	@tag.name	Reviews

//	@tag.name	Sessions

//	@tag.name	Organization Management

//	@tag.name	Reports

//	@tag.name	Audit Logs
//	@tag.description.markdown

// @securitydefinitions.oauth2.accessCode	OAuth2AccessCode
// @tokenUrl								https://login.microsoftonline.com/d60ba6f0-ad5f-4917-aa19-f8d4241f8bc7/oauth2/v2.0/token
// @authorizationUrl						https://login.microsoftonline.com/d60ba6f0-ad5f-4917-aa19-f8d4241f8bc7/oauth2/v2.0/authorize
// @scope.profile
// @scope.email
// @scope.openid
func (a *Api) StartAPI() {
	if os.Getenv("PORT") == "" {
		os.Setenv("PORT", "8009")
	}
	zaplogger := log.NewDefaultLogger(nil)
	defer zaplogger.Sync()
	route := gin.New()
	route.Use(ginzap.RecoveryWithZap(zaplogger, false))
	if os.Getenv("GIN_MODE") == "debug" {
		route.Use(ginzap.Ginzap(zaplogger, time.RFC3339, true))
	}
	a.logger = zaplogger
	// https://pkg.go.dev/github.com/gin-gonic/gin#readme-don-t-trust-all-proxies
	route.SetTrustedProxies(nil)
	route.Use(SecurityHeaderMiddleware())
	route.Use(CORSMiddleware())
	baseURL := appconfig.Get().ApiURLPath()

	// UI
	webappStaticUiPath := appconfig.Get().WebappStaticUiPath()
	route.Use(static.Serve(baseURL+"/", static.LocalFile(webappStaticUiPath, false)))
	route.NoRoute(func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.RequestURI, baseURL+"/api") {
			c.File(fmt.Sprintf("%s/index.html", webappStaticUiPath))
			return
		}
	})

	ssmGroup := route.Group(baseURL + "/ssm")
	ssmInstance := ssmproxy.GetServerInstance()
	ssmInstance.AttachHandlers(ssmGroup)

	ironRdpGroup := route.Group(baseURL + "/rdpproxy")
	ironRdpInstance := rdp.GetIronServerInstance()
	ironRdpInstance.AttachHandlers(ironRdpGroup)

	rg := route.Group(baseURL + "/api")
	rg.Use(sentrygin.New(sentrygin.Options{Repanic: true}))
	rg.Use(sentryCatchAll5xxMiddleware)

	router := apiroutes.New(rg)

	a.buildRoutes(router)
	openapi.RegisterGinValidators()

	if a.TLSConfig != nil {
		server := http.Server{
			Addr:      "0.0.0.0:8009",
			Handler:   route,
			TLSConfig: a.TLSConfig,
			ErrorLog:  log.NewStdHttpLogger(),
		}
		if err := server.ListenAndServeTLS("", ""); err != nil {
			log.Fatalf("Failed to start HTTPS server, err=%v", err)
		}
		return
	}
	if err := route.Run(); err != nil {
		log.Fatalf("Failed to start HTTP server, err=%v", err)
	}
}

func (api *Api) buildRoutes(r *apiroutes.Router) {
	reviewHandler := reviewapi.NewHandler(api.ReleaseConnectionFn)
	loginOidcApiHandler := loginoidcapi.New()
	loginSamlApiHandler := loginsamlapi.New()

	r.GET("/healthz", apihealthz.LivenessHandler())
	r.GET("/openapiv2.json", openapi.Handler)
	r.GET("/openapiv3.json", openapi.HandlerV3)

	r.GET("/publicserverinfo", apipublicserverinfo.Get)
	r.GET("/serverinfo",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiserverinfo.Get)

	// Ouath2 / OIDC
	r.GET("/login", loginOidcApiHandler.Login)
	r.GET("/callback", loginOidcApiHandler.LoginCallback)

	// SAML 2.0
	r.GET("/saml/login", loginSamlApiHandler.SamlLogin)
	r.POST("/saml/callback", loginSamlApiHandler.SamlLoginCallback)

	r.POST("/localauth/register",
		api.TrackRequest(analytics.EventSignup),
		loginlocalapi.Register)
	r.POST("/localauth/login",
		api.TrackRequest(analytics.EventLogin),
		loginlocalapi.Login)

	r.POST("/signup",
		api.TrackRequest(analytics.EventSignup),
		signupapi.Post)

	r.GET("/userinfo",
		apiroutes.UserInfoRouteType,
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		userapi.GetUserInfo)
	r.GET("/users",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		userapi.List)
	r.GET("/users/:emailOrID",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		userapi.GetUserByEmailOrID)
	r.POST("/users",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		userapi.Create)
	r.PUT("/users/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventUpdateUser),
		userapi.Update)
	r.PATCH("/users/self/slack",
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventUpdateUser),
		userapi.PatchSlackID)
	r.DELETE("/users/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		userapi.Delete)

	r.GET("/users/groups",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		userapi.ListAllGroups)
	r.POST("/users/groups",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		userapi.CreateGroup)
	r.DELETE("/users/groups/:name",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		userapi.DeleteGroup)

	r.GET("/serviceaccounts",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		serviceaccountapi.List)
	r.POST("/serviceaccounts",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventCreateServiceAccount),
		serviceaccountapi.Create)
	r.PUT("/serviceaccounts/:subject",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventCreateServiceAccount),
		serviceaccountapi.Update)

	r.GET("/api-keys",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apikeys.List)
	r.GET("/api-keys/:nameOrID",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apikeys.Get)
	r.POST("/api-keys",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventCreateApiKey),
		apikeys.Create)
	r.PUT("/api-keys/:nameOrID",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventUpdateApiKey),
		apikeys.Update)
	r.DELETE("/api-keys/:nameOrID",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventRevokeApiKey),
		apikeys.Revoke)
	r.POST("/api-keys/:nameOrID/reactivate",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventReactivateApiKey),
		apikeys.Reactivate)

	r.POST("/connections",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventCreateConnection),
		apiconnections.Post)
	r.PUT("/connections/:nameOrID",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventUpdateConnection),
		apiconnections.Put)
	r.PATCH("/connections/:nameOrID",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventUpdateConnection),
		apiconnections.Patch)
	r.GET("/connections",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiconnections.List)
	r.GET("/connections/:nameOrID",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiconnections.Get)
	r.DELETE("/connections/:name",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventDeleteConnection),
		apiconnections.Delete)
	r.GET("/connections/:nameOrID/databases",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiconnections.ListDatabases)
	r.GET("/connections/:nameOrID/tables",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiconnections.ListTables)
	r.GET("/connections/:nameOrID/columns",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiconnections.GetTableColumns)
	r.PUT("/connections/:nameOrID/datamasking-rules",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiconnections.UpdateDataMaskingRuleConnection)

	r.GET("/connections/:nameOrID/ai-session-analyzer-rule",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiai.GetConnectionAnalyzerRule)

	r.GET("/connections/:nameOrID/test",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiconnections.TestConnection)
	r.POST("/connections/:nameOrID/credentials",
		r.AuthMiddleware,
		apiconnections.CreateConnectionCredentials,
	)
	r.POST("/connections/:nameOrID/credentials/:ID",
		r.AuthMiddleware,
		apiconnections.ResumeConnectionCredentials,
	)
	r.POST("/connections/:nameOrID/credentials/:ID/revoke",
		r.AuthMiddleware,
		apiconnections.RevokeConnectionCredentials,
	)
	r.POST("/connections/:nameOrID/credentials/:ID/close",
		r.AuthMiddleware,
		apiconnections.CloseConnectionCredentials,
	)

	r.GET("/connection-tags",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiconnections.ListTags,
	)

	r.POST("/proxymanager/connect",
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventApiProxymanagerConnect),
		apiproxymanager.Post)
	r.POST("/proxymanager/disconnect",
		r.AuthMiddleware,
		apiproxymanager.Disconnect)
	r.GET("/proxymanager/status",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiproxymanager.Get)

	r.GET("/reviews",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventFetchReviews),
		reviewHandler.List,
	)
	r.GET("/reviews/:id",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventFetchReviews),
		reviewHandler.GetByIdOrSid,
	)
	r.PUT("/reviews/:id",
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventUpdateReview),
		reviewHandler.ReviewByIdOrSid,
	)

	r.GET("/access-requests/rules",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		accessrequestsapi.ListAccessRequestRules,
	)
	r.POST("/access-requests/rules",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		accessrequestsapi.CreateAccessRequestRule,
	)
	r.GET("/access-requests/rules/:name",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		accessrequestsapi.GetAccessRequestRule,
	)
	r.PUT("/access-requests/rules/:name",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		accessrequestsapi.UpdateAccessRequestRule,
	)
	r.DELETE("/access-requests/rules/:name",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		accessrequestsapi.DeleteAccessRequestRule,
	)

	r.POST("/agents",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventCreateAgent),
		apiagents.Post)
	r.GET("/agents",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiagents.List)
	r.GET("/agents/:nameOrID",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiagents.Get)
	r.DELETE("/agents/:nameOrID",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventDeleteAgent),
		apiagents.Delete)

	r.POST("/orgs/keys",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		apiorgs.CreateAgentKey)
	r.GET("/orgs/keys",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		apiorgs.GetAgentKey)
	r.DELETE("/orgs/keys",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		apiorgs.RevokeAgentKey)

	r.PUT("/orgs/license",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiorgs.UpdateOrgLicense)
	r.POST("/orgs/license/sign",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiorgs.SignLicense)

	r.PUT("/orgs/features",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventOrgFeatureUpdate),
		apifeatures.FeatureUpdate)

	r.POST("/features/ask-ai/v1/chat/completions",
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventFeatureAskAIChatCompletions),
		apifeatures.PostChatCompletions)

	r.POST("/plugins",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventCreatePlugin),
		apiplugins.Post)
	r.PUT("/plugins/:name",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventUpdatePlugin),
		apiplugins.Put)
	r.PUT("/plugins/:name/config",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventUpdatePluginConfig),
		apiplugins.PutConfig)
	r.GET("/plugins",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiplugins.List)
	r.GET("/plugins/:name",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiplugins.Get)

	// the resource conn is used to avoid conflict with /plugins/runbooks/connections route

	r.PUT("/plugins/:name/conn/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apipluginconnections.UpsertPluginConnection)
	r.GET("/plugins/:name/conn/:id",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		apipluginconnections.GetPluginConnection)
	r.DELETE("/plugins/:name/conn/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apipluginconnections.DeletePluginConnection)

	// alias routes
	r.GET("/plugins/audit/sessions/:session_id",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		sessionapi.Get)
	r.GET("/plugins/audit/sessions",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		sessionapi.List)

	r.GET("/sessions/:session_id",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		sessionapi.Get)
	r.GET("/sessions/:session_id/download", sessionapi.DownloadSession)
	r.GET("/sessions/:session_id/download/input", sessionapi.DownloadSessionInput)

	r.GET("/sessions/:session_id/rdp-frames",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		sessionapi.GetRDPFrames)

	r.GET("/sessions/:session_id/rdp-detections",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		sessionapi.GetRDPDetections)

	r.GET("/sessions/:session_id/result/stream",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		sessionapi.StreamSessionResult)

	r.POST("/sessions/:session_id/kill",
		r.AuthMiddleware,
		sessionapi.Kill)
	r.PUT("/sessions/:session_id/review",
		r.AuthMiddleware,
		reviewHandler.ReviewBySid,
	)
	r.PATCH("/sessions/:session_id/metadata",
		r.AuthMiddleware,
		sessionapi.PatchMetadata)
	r.GET("/sessions",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		sessionapi.List)
	r.POST("/sessions",
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventApiExecSession),
		sessionapi.Post)
	r.POST("/sessions/:session_id/exec",
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventApiExecReview),
		sessionapi.RunReviewedExec)

	r.POST("/sessions/provision",
		r.AuthMiddleware,
		r.OnlyApiKeyAccess,
		sessionapi.Provision)

	r.GET("/reports/sessions",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apireports.SessionReport)

	r.GET("/plugins/runbooks/connections/:name/templates",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apirunbooks.ListByConnection)
	r.GET("/plugins/runbooks/templates",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apirunbooks.List)

	r.POST("/plugins/runbooks/connections/:name/exec",
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventExecRunbook),
		apirunbooks.RunExec)

	r.GET("/webhooks-dashboard",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventOpenWebhooksDashboard),
		webhooksapi.GetDashboardURL)

	// svix experimental routes (endpoints)
	r.GET("/webhooks/endpoints",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		webhooksapi.ListSvixEndpoints,
	)
	r.GET("/webhooks/endpoints/:id",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		webhooksapi.GetSvixEndpointByID,
	)
	r.POST("/webhooks/endpoints",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		webhooksapi.CreateSvixEndpoint,
	)
	r.PUT("/webhooks/endpoints/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		webhooksapi.UpdateSvixEndpoint,
	)
	r.DELETE("/webhooks/endpoints/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		webhooksapi.DeleteSvixEndpointByID,
	)

	// svix experimental routes (event types)
	r.POST("/webhooks/eventtypes",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		webhooksapi.CreateSvixEventType,
	)
	r.PUT("/webhooks/eventtypes/:name",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		webhooksapi.UpdateSvixEventType,
	)
	r.GET("/webhooks/eventtypes",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		webhooksapi.ListSvixEventTypes,
	)
	r.GET("/webhooks/eventtypes/:name",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		webhooksapi.GetSvixEventTypeByName,
	)
	r.DELETE("/webhooks/eventtypes/:name",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		webhooksapi.DeleteSvixEventType,
	)

	// svix experimental routes (messages)
	r.POST("/webhooks/messages",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		webhooksapi.CreateSvixMessage,
	)
	r.GET("/webhooks/messages",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		webhooksapi.ListSvixMessages,
	)
	r.GET("/webhooks/messages/:id",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		webhooksapi.GetSvixMessageByID,
	)

	// Jira Integration routes
	r.GET("/integrations/jira",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apijiraintegration.Get)
	r.POST("/integrations/jira",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventCreateJiraIntegration),
		apijiraintegration.Post)
	r.PUT("/integrations/jira",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventUpdateJiraIntegration),
		apijiraintegration.Put)

	// Jira Integration Issue Templates routes
	r.POST("/integrations/jira/issuetemplates",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apijiraintegration.CreateIssueTemplates,
	)
	r.PUT("/integrations/jira/issuetemplates/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apijiraintegration.UpdateIssueTemplates,
	)
	r.GET("/integrations/jira/issuetemplates",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apijiraintegration.ListIssueTemplates,
	)
	r.GET("/integrations/jira/issuetemplates/:id",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apijiraintegration.GetIssueTemplatesByID,
	)
	r.DELETE("/integrations/jira/issuetemplates/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apijiraintegration.DeleteIssueTemplates,
	)

	r.GET("/integrations/jira/assets/objects",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apijiraintegration.GetAssetObjects)

	// AWS routes
	r.GET("/integrations/aws/iam/userinfo",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		awsintegration.IAMGetUserInfo)

	r.PUT("/integrations/aws/iam/accesskeys",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		awsintegration.IAMUpdateAccessKey)

	r.DELETE("/integrations/aws/iam/accesskeys",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		awsintegration.IAMDeleteAccessKey)

	r.GET("/integrations/aws/organizations",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		// api.TrackRequest,
		awsintegration.ListOrganizations)

	r.POST("/integrations/aws/rds/describe-db-instances",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		awsintegration.DescribeRDSDBInstances)

	r.POST("/integrations/aws/rds/credentials",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		awsintegration.CreateRDSRootPassword)

	r.POST("/dbroles/jobs",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		awsintegration.CreateDBRoleJob,
	)

	r.GET("/dbroles/jobs",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		awsintegration.ListDBRoleJobs,
	)

	r.GET("/dbroles/jobs/:id",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		awsintegration.GetDBRoleJobByID,
	)

	r.GET("/ai/session-analyzer/providers",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		apiai.GetSessionAnalyzerProvider)
	r.POST("/ai/session-analyzer/providers",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiai.UpsertSessionAnalyzerProvider)
	r.DELETE("/ai/session-analyzer/providers",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiai.DeleteSessionAnalyzerProvider)

	r.GET("/ai/session-analyzer/rules",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		apiai.ListSessionAnalyzerRules)
	r.POST("/ai/session-analyzer/rules",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiai.CreateSessionAnalyzerRule)
	r.GET("/ai/session-analyzer/rules/:name",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		apiai.GetSessionAnalyzerRule)
	r.PUT("/ai/session-analyzer/rules/:name",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiai.UpdateSessionAnalyzerRule)
	r.DELETE("/ai/session-analyzer/rules/:name",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiai.DeleteSessionAnalyzerRule)

	r.POST("/guardrails",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventCreateGuardRailRules),
		apiguardrails.Post)
	r.PUT("/guardrails/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventUpdateGuardRailRules),
		apiguardrails.Put)
	r.GET("/guardrails",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		apiguardrails.List)
	r.GET("/guardrails/:id",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		apiguardrails.Get)
	r.DELETE("/guardrails/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		api.TrackRequest(analytics.EventDeleteGuardRailRules),
		apiguardrails.Delete)

	r.POST("/datamasking-rules",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		apidatamasking.Post)
	r.PUT("/datamasking-rules/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		apidatamasking.Put)
	r.GET("/datamasking-rules",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		apidatamasking.List)
	r.GET("/datamasking-rules/:id",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		apidatamasking.Get)
	r.DELETE("/datamasking-rules/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		apidatamasking.Delete)

	// server config routes
	r.GET("/serverconfig/misc",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		apiserverconfig.GetServerMisc,
	)
	r.PUT("/serverconfig/misc",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiserverconfig.UpdateServerMisc,
	)
	r.GET("/serverconfig/auth",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		apiserverconfig.GetAuthConfig,
	)
	r.PUT("/serverconfig/auth",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.AuditMiddleware(),
		apiserverconfig.UpdateAuthConfig,
	)
	r.POST("/serverconfig/auth/apikey",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiserverconfig.GenerateApiKey,
	)

	r.GET("/search",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		searchapi.Get,
	)

	r.GET("/metrics/sessions",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		metricsapi.Get,
	)

	r.POST("/runbooks/configurations",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apirunbooks.CreateRunbookConfigurationEntry,
	)
	r.PUT("/runbooks/configurations/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apirunbooks.UpdateRunbookConfigurationEntry,
	)
	r.DELETE("/runbooks/configurations/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apirunbooks.DeleteRunbookConfiguration,
	)
	r.POST("/runbooks/configurations/:id/files",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apirunbooks.CreateRunbookFile,
	)
	r.GET("/runbooks",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apirunbooks.ListRunbooksV2,
	)
	r.POST("/runbooks/exec",
		r.AuthMiddleware,
		apirunbooks.RunbookExec,
	)
	r.GET("/runbooks/configurations",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		apirunbooks.GetRunbookConfiguration,
	)
	r.PUT("/runbooks/configurations",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apirunbooks.UpdateRunbookConfiguration,
	)

	r.GET("/runbooks/rules",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apirunbooks.ListRunbookRules,
	)
	r.GET("/runbooks/rules/:id",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apirunbooks.GetRunbookRule,
	)
	r.POST("/runbooks/rules",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apirunbooks.CreateRunbookRule,
	)
	r.PUT("/runbooks/rules/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apirunbooks.UpdateRunbookRule,
	)
	r.DELETE("/runbooks/rules/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apirunbooks.DeleteRunbookRule,
	)

	r.GET("/ws", transport.HandleConnection)

	r.GET("/resources",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		resourcesapi.ListResources)
	r.GET("/resources/:name",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		resourcesapi.GetResource)
	r.POST("/resources",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		resourcesapi.CreateResource)
	r.PUT("/resources/:name",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		resourcesapi.UpdateResource)
	r.DELETE("/resources/:name",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		resourcesapi.DeleteResource)

	r.GET("/audit/logs",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		auditlogapi.List)

	r.GET("/attributes",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiattributes.List)
	r.GET("/attributes/:name",
		apiroutes.AdminAndAuditorAccessRole,
		r.AuthMiddleware,
		apiattributes.Get)
	r.POST("/attributes",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiattributes.Post)
	r.PUT("/attributes/:name",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiattributes.Put)
	r.DELETE("/attributes/:name",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiattributes.Delete)

	// MCP Server — uses Any() because MCP protocol uses POST, GET, and DELETE on the same path
	mcpServer := apimcpserver.New(api.ReleaseConnectionFn)
	r.RouterGroup.Any("/mcp", r.AuthMiddleware, api.AuditMiddleware(), mcpServer.GinHandler)
}
