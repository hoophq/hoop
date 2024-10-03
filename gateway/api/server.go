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
	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	apifeatures "github.com/hoophq/hoop/gateway/api/features"
	apihealthz "github.com/hoophq/hoop/gateway/api/healthz"
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
	a.buildRoutes(rg)
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

func (api *Api) buildRoutes(route *gin.RouterGroup) {
	// set standard role to all routes
	route.Use(StandardAccessRole)

	reviewHandler := reviewapi.NewHandler(&api.ReviewHandler)
	loginHandler := loginapi.New(api.IDProvider)
	route.GET("/openapiv2.json", openapi.Handler)
	route.GET("/openapiv3.json", openapi.HandlerV3)
	route.GET("/login", loginHandler.Login)
	route.GET("/callback", loginHandler.LoginCallback)
	route.GET("/healthz", apihealthz.LivenessHandler())
	///////////////////////
	// local auth routes //
	///////////////////////
	route.POST("/localauth/register",
		api.LocalAuthOnly,
		api.TrackRequest(analytics.EventSignup),
		localauthapi.Register)
	route.POST("/localauth/login",
		api.LocalAuthOnly,
		api.TrackRequest(analytics.EventLogin),
		localauthapi.Login)
	///////////////////////////
	// end local auth routes //
	///////////////////////////
	route.POST("/signup",
		AnonAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventSignup),
		signupapi.Post)
	route.GET("/users",
		api.AllowApiKey,
		AdminOnlyAccessRole,
		api.Authenticate,
		userapi.List)
	route.GET("/users/:emailOrID",
		AdminOnlyAccessRole,
		api.Authenticate,
		userapi.GetUserByEmailOrID)
	route.GET("/userinfo",
		AnonAccessRole,
		api.Authenticate,
		userapi.GetUserInfo)
	route.PATCH("/users/self/slack",
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdateUser),
		AuditApiChanges,
		userapi.PatchSlackID)
	route.GET("/users/groups",
		api.AllowApiKey,
		api.Authenticate,
		userapi.ListAllGroups)
	route.PUT("/users/:id",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdateUser),
		AuditApiChanges,
		userapi.Update)
	route.POST("/users",
		AdminOnlyAccessRole,
		api.Authenticate,
		AuditApiChanges,
		userapi.Create)
	route.DELETE("/users/:id",
		AdminOnlyAccessRole,
		api.Authenticate,
		AuditApiChanges,
		userapi.Delete)

	route.GET("/serviceaccounts",
		AdminOnlyAccessRole,
		api.Authenticate,
		serviceaccountapi.List)
	route.POST("/serviceaccounts",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateServiceAccount),
		AuditApiChanges,
		serviceaccountapi.Create)
	route.PUT("/serviceaccounts/:subject",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateServiceAccount),
		AuditApiChanges,
		serviceaccountapi.Update)

	route.POST("/connections",
		api.AllowApiKey,
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateConnection),
		AuditApiChanges,
		apiconnections.Post)
	route.PUT("/connections/:nameOrID",
		api.AllowApiKey,
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdateConnection),
		AuditApiChanges,
		apiconnections.Put)
	// DEPRECATED in flavor of POST /sessions
	route.POST("/connections/:name/exec",
		api.Authenticate,
		api.TrackRequest(analytics.EventApiExecConnection),
		sessionapi.Post)
	route.GET("/connections",
		api.AllowApiKey,
		api.Authenticate,
		apiconnections.List)
	route.GET("/connections/:nameOrID",
		api.AllowApiKey,
		api.Authenticate,
		apiconnections.Get)
	route.DELETE("/connections/:name",
		api.AllowApiKey,
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventDeleteConnection),
		AuditApiChanges,
		apiconnections.Delete)

	route.POST("/proxymanager/connect",
		api.Authenticate,
		api.TrackRequest(analytics.EventApiProxymanagerConnect),
		apiproxymanager.Post,
	)
	route.POST("/proxymanager/disconnect",
		api.Authenticate,
		apiproxymanager.Disconnect,
	)
	route.GET("/proxymanager/status",
		api.Authenticate,
		apiproxymanager.Get,
	)

	route.GET("/reviews",
		api.AllowApiKey,
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchReviews),
		reviewHandler.List)
	route.GET("/reviews/:id",
		api.AllowApiKey,
		api.Authenticate,
		api.TrackRequest(analytics.EventFetchReviews),
		reviewHandler.Get)
	route.PUT("/reviews/:id",
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdateReview),
		AuditApiChanges,
		reviewHandler.Put)

	route.POST("/agents",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventCreateAgent),
		AuditApiChanges,
		apiagents.Post)
	route.GET("/agents",
		api.AllowApiKey,
		AdminOnlyAccessRole,
		api.Authenticate,
		apiagents.List)
	route.DELETE("/agents/:nameOrID",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventDeleteAgent),
		AuditApiChanges,
		apiagents.Delete)

	route.POST("/orgs/keys",
		AdminOnlyAccessRole,
		api.Authenticate,
		AuditApiChanges,
		apiorgs.CreateAgentKey)
	route.GET("/orgs/keys",
		api.AllowApiKey,
		AdminOnlyAccessRole,
		api.Authenticate,
		apiorgs.GetAgentKey)
	route.DELETE("/orgs/keys",
		AdminOnlyAccessRole,
		api.Authenticate,
		apiorgs.RevokeAgentKey)

	route.PUT("/orgs/license",
		AdminOnlyAccessRole,
		api.Authenticate,
		AuditApiChanges,
		apiorgs.UpdateOrgLicense)
	route.POST("/orgs/license/sign",
		AdminOnlyAccessRole,
		api.Authenticate,
		AuditApiChanges,
		apiorgs.SignLicense)

	route.PUT("/orgs/features",
		api.AllowApiKey,
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventOrgFeatureUpdate),
		AuditApiChanges,
		apifeatures.FeatureUpdate)

	route.POST("/features/ask-ai/v1/chat/completions",
		api.Authenticate,
		api.TrackRequest(analytics.EventFeatureAskAIChatCompletions),
		AuditApiChanges,
		apifeatures.PostChatCompletions)

	route.POST("/plugins",
		api.AllowApiKey,
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventCreatePlugin),
		AuditApiChanges,
		apiplugins.Post)
	route.PUT("/plugins/:name",
		api.AllowApiKey,
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdatePlugin),
		AuditApiChanges,
		apiplugins.Put)
	route.GET("/plugins",
		api.AllowApiKey,
		api.Authenticate,
		apiplugins.List)
	route.GET("/plugins/:name",
		api.AllowApiKey,
		api.Authenticate,
		apiplugins.Get)
	route.PUT("/plugins/:name/config",
		api.AllowApiKey,
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventUpdatePluginConfig),
		AuditApiChanges,
		apiplugins.PutConfig)

	// alias routes
	route.GET("/plugins/audit/sessions/:session_id",
		api.Authenticate,
		sessionapi.Get)
	route.GET("/plugins/audit/sessions",
		api.Authenticate,
		sessionapi.List)

	route.GET("/sessions/:session_id",
		api.AllowApiKey,
		api.Authenticate,
		sessionapi.Get)
	route.GET("/sessions/:session_id/download", sessionapi.DownloadSession)
	route.GET("/sessions",
		api.AllowApiKey,
		api.Authenticate,
		sessionapi.List)
	route.POST("/sessions",
		api.Authenticate,
		api.TrackRequest(analytics.EventApiExecSession),
		sessionapi.Post)
	route.POST("/sessions/:session_id/exec",
		api.Authenticate,
		api.TrackRequest(analytics.EventApiExecReview),
		sessionapi.RunReviewedExec)

	route.GET("/reports/sessions",
		api.AllowApiKey,
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventApiExecReview),
		apireports.SessionReport)

	route.POST("/plugins/indexer/sessions/search",
		api.Authenticate,
		api.TrackRequest(analytics.EventSearch),
		api.IndexerHandler.Search,
	)

	route.GET("/plugins/runbooks/connections/:name/templates",
		api.AllowApiKey,
		api.Authenticate,
		apirunbooks.ListByConnection,
	)

	route.GET("/plugins/runbooks/templates",
		api.AllowApiKey,
		api.Authenticate,
		apirunbooks.List,
	)

	route.POST("/plugins/runbooks/connections/:name/exec",
		api.AllowApiKey,
		api.Authenticate,
		api.TrackRequest(analytics.EventExecRunbook),
		apirunbooks.RunExec)

	route.GET("/webhooks-dashboard",
		AdminOnlyAccessRole,
		api.Authenticate,
		api.TrackRequest(analytics.EventOpenWebhooksDashboard),
		AuditApiChanges,
		webhooksapi.Get)

	route.GET("/publicserverinfo", apipublicserverinfo.Get)

	route.GET("/serverinfo",
		api.AllowApiKey,
		api.Authenticate,
		apiserverinfo.New(api.GrpcURL).Get)
}
