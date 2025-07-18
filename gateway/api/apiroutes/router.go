package apiroutes

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/appconfig"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

const (
	routeTypeContextKey string = "route-type"
	routeUserInfoType   string = "userinfo"
)

// UserInfoRouteType is a special route that validates if a user is authenticated and registered or
// if it's an authenticated by validating in the Oauth2 user info endpoint
func UserInfoRouteType(c *gin.Context) {
	c.Set(routeTypeContextKey, routeUserInfoType)
	c.Next()
}

func routeTypeFromContext(c *gin.Context) string {
	obj, ok := c.Get(routeTypeContextKey)
	if !ok {
		return ""
	}
	routeType, _ := obj.(string)
	return routeType
}

type Router struct {
	*gin.RouterGroup
	apiURL string
}

func New(route *gin.RouterGroup) *Router {
	if route == nil {
		log.Fatalf("route is nil")
	}

	route.Use(otelgin.Middleware("hoopgateway",
		otelgin.WithFilter(func(r *http.Request) bool {
			return r.RequestURI != "/api/healthz"
		}),
	))
	route.Use(contextTracerMiddleware())
	return &Router{
		RouterGroup: route,
		apiURL:      appconfig.Get().ApiURL(),
	}
}

func (r Router) GET(relativePath string, handlers ...gin.HandlerFunc) gin.IRoutes {
	return r.RouterGroup.GET(relativePath, handlers...).
		HEAD(relativePath, handlers...)
}
