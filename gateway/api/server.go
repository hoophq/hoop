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
	"go.uber.org/zap"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/analytics"
	apiagents "github.com/hoophq/hoop/gateway/api/agents"
	"github.com/hoophq/hoop/gateway/api/apiroutes"
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	apifeatures "github.com/hoophq/hoop/gateway/api/features"
	apiguardrails "github.com/hoophq/hoop/gateway/api/guardrails"
	apihealthz "github.com/hoophq/hoop/gateway/api/healthz"
	apijiraintegration "github.com/hoophq/hoop/gateway/api/integrations"
	localauthapi "github.com/hoophq/hoop/gateway/api/localauth"
	loginapi "github.com/hoophq/hoop/gateway/api/login"
	"github.com/hoophq/hoop/gateway/api/openapi"
	apiorgs "github.com/hoophq/hoop/gateway/api/orgs"
	apiplugins "github.com/hoophq/hoop/gateway/api/plugins"
	apiproxymanager "github.com/hoophq/hoop/gateway/api/proxymanager"
	apipublicserverinfo "github.com/hoophq/hoop/gateway/api/publicserverinfo"
	apireports "github.com/hoophq/hoop/gateway/api/reports"
	reviewapi "github.com/hoophq/hoop/gateway/api/review"
	apirunbooks "github.com/hoophq/hoop/gateway/api/runbooks"
	apiserverinfo "github.com/hoophq/hoop/gateway/api/serverinfo"
	serviceaccountapi "github.com/hoophq/hoop/gateway/api/serviceaccount"
	sessionapi "github.com/hoophq/hoop/gateway/api/session"
	signupapi "github.com/hoophq/hoop/gateway/api/signup"
	userapi "github.com/hoophq/hoop/gateway/api/user"
	webhooksapi "github.com/hoophq/hoop/gateway/api/webhooks"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/indexer"
	"github.com/hoophq/hoop/gateway/review"
	"github.com/hoophq/hoop/gateway/security/idp"
)

type Api struct {
	IndexerHandler indexer.Handler
	ReviewHandler  review.Handler
	IDProvider     *idp.Provider
	GrpcURL        string
	TLSConfig      *tls.Config
	logger         *zap.Logger
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

//	@tag.name	Core
//	@tag.description.markdown

//	@tag.name	User Management
//	@tag.description.markdown

//	@tag.name	Server Management
//	@tag.description.markdown

//	@tag.name	Features
//	@tag.description.markdown

//	@tag.name	Proxy Manager
//	@tag.description.markdown

// @securitydefinitions.oauth2.accessCode	OAuth2AccessCode
// @tokenUrl								https://login.microsoftonline.com/d60ba6f0-ad5f-4917-aa19-f8d4241f8bc7/oauth2/v2.0/token
// @authorizationUrl						https://login.microsoftonline.com/d60ba6f0-ad5f-4917-aa19-f8d4241f8bc7/oauth2/v2.0/authorize
// @scope.profile
// @scope.email
// @scope.openid
func (a *Api) StartAPI(sentryInit bool) {
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

	rg := route.Group(baseURL + "/api")
	if sentryInit {
		rg.Use(sentrygin.New(sentrygin.Options{
			Repanic: true,
		}))
	}
	router := apiroutes.New(rg, a.IDProvider, appconfig.Get().GrpcURL(), appconfig.Get().ApiKey())
	a.buildRoutes(router)

	if a.TLSConfig != nil {
		server := http.Server{
			Addr:      "0.0.0.0:8009",
			Handler:   route,
			TLSConfig: a.TLSConfig,
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
	reviewHandler := reviewapi.NewHandler(&api.ReviewHandler)
	loginHandler := loginapi.New(api.IDProvider)

	r.GET("/healthz", apihealthz.LivenessHandler())
	r.GET("/openapiv2.json", openapi.Handler)
	r.GET("/openapiv3.json", openapi.HandlerV3)

	r.GET("/publicserverinfo", apipublicserverinfo.Get)
	r.GET("/serverinfo",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiserverinfo.New(api.GrpcURL).Get)

	r.GET("/login", loginHandler.Login)
	r.GET("/callback", loginHandler.LoginCallback)

	r.POST("/localauth/register",
		api.TrackRequest(analytics.EventSignup),
		localauthapi.Register)
	r.POST("/localauth/login",
		api.TrackRequest(analytics.EventLogin),
		localauthapi.Login)

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
	r.PATCH("/users/self/slack",
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventUpdateUser),
		userapi.PatchSlackID)
	r.GET("/users/groups",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		userapi.ListAllGroups)
	r.POST("/users",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		userapi.Create)
	r.DELETE("/users/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		userapi.Delete)
	r.PUT("/users/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventUpdateUser),
		userapi.Update)

	r.GET("/serviceaccounts",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		serviceaccountapi.List)
	r.POST("/serviceaccounts",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventCreateServiceAccount),
		serviceaccountapi.Create)
	r.PUT("/serviceaccounts/:subject",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventCreateServiceAccount),
		serviceaccountapi.Update)

	r.POST("/connections",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventCreateConnection),
		apiconnections.Post)
	r.PUT("/connections/:nameOrID",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventUpdateConnection),
		apiconnections.Put)
	// DEPRECATED in flavor of POST /sessions
	r.POST("/connections/:name/exec",
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventApiExecConnection),
		sessionapi.Post,
	)
	r.GET("/connections",
		r.AuthMiddleware,
		apiconnections.List)
	r.GET("/connections/:nameOrID",
		r.AuthMiddleware,
		apiconnections.Get)
	r.DELETE("/connections/:name",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventDeleteConnection),
		apiconnections.Delete)
	r.GET("/connections/:nameOrID/databases",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiconnections.ListDatabases)
	r.GET("/connections/:nameOrID/schemas",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiconnections.GetDatabaseSchemas)

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
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventFetchReviews),
		reviewHandler.List)
	r.GET("/reviews/:id",
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventFetchReviews),
		reviewHandler.Get)
	r.PUT("/reviews/:id",
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventUpdateReview),
		reviewHandler.Put)

	r.POST("/agents",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventCreateAgent),
		apiagents.Post)
	r.GET("/agents",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		apiagents.List)
	r.DELETE("/agents/:nameOrID",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventDeleteAgent),
		apiagents.Delete)

	r.POST("/orgs/keys",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiorgs.CreateAgentKey)
	r.GET("/orgs/keys",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apiorgs.GetAgentKey)
	r.DELETE("/orgs/keys",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
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
	r.PUT("/sessions/:session_id/review",
		r.AuthMiddleware,
		reviewHandler.ReviewBySession)
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

	r.GET("/reports/sessions",
		apiroutes.ReadOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventApiExecReview),
		apireports.SessionReport)

	r.POST("/plugins/indexer/sessions/search",
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventSearch),
		api.IndexerHandler.Search,
	)

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
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventOpenWebhooksDashboard),
		webhooksapi.Get)

	// Jira Integration routes
	r.GET("/integrations/jira",
		apiroutes.AdminOnlyAccessRole,
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
		r.AuthMiddleware,
		apijiraintegration.ListIssueTemplates,
	)
	r.GET("/integrations/jira/issuetemplates/:id",
		r.AuthMiddleware,
		apijiraintegration.GetIssueTemplatesByID,
	)
	r.DELETE("/integrations/jira/issuetemplates/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		apijiraintegration.DeleteIssueTemplates,
	)

	r.POST("/guardrails",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventCreateGuardRailRules),
		apiguardrails.Post)
	r.PUT("/guardrails/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventUpdateGuardRailRules),
		apiguardrails.Put)
	r.GET("/guardrails",
		r.AuthMiddleware,
		apiguardrails.List)
	r.GET("/guardrails/:id",
		r.AuthMiddleware,
		apiguardrails.Get)
	r.DELETE("/guardrails/:id",
		apiroutes.AdminOnlyAccessRole,
		r.AuthMiddleware,
		api.TrackRequest(analytics.EventDeleteGuardRailRules),
		apiguardrails.Delete)
}
