package apiroutes

import (
	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/storagev2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var vs = version.Get()

func contextTracerMiddleware() gin.HandlerFunc {
	conf := appconfig.Get()
	environment := conf.ApiHostname()
	tenancyType := "selfhosted"
	if conf.OrgMultitenant() {
		tenancyType = "multitenant"
	}
	authMethod := conf.AuthMethod()
	return func(c *gin.Context) {
		defer c.Next()
		span := trace.SpanFromContext(c.Request.Context())
		if !span.IsRecording() {
			return
		}
		span.SetAttributes(
			attribute.String("hoop.gateway.environment", environment),
			attribute.String("hoop.gateway.tenancy-type", tenancyType),
			attribute.String("hoop.gateway.auth-method", string(authMethod)),
			attribute.String("hoop.gateway.platform", vs.Platform),
			attribute.String("hoop.gateway.version", vs.Version),
		)
		obj, ok := c.Get(storagev2.ContextKey)
		if !ok {
			return
		}
		if ctx, ok := obj.(*storagev2.Context); ok {
			span.SetAttributes(
				attribute.String("hoop.gateway.org-id", ctx.OrgID),
				attribute.String("hoop.gateway.user-email", ctx.UserEmail),
				attribute.String("hoop.gateway.user-slackid", ctx.SlackID),
				attribute.String("hoop.gateway.user-grouprole", ctx.GroupRoleName()),
				attribute.Int("hoop.gateway.user-groups-size", len(ctx.UserGroups)),
			)
		}

	}
}

func SetSidSpanAttr(c *gin.Context, sid string) {
	if span := trace.SpanFromContext(c.Request.Context()); span != nil && sid != "" {
		span.SetAttributes(attribute.String("hoop.gateway.sid", sid))
	}
}
