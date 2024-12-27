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
	grpcURL := conf.GrpcURL()
	tenancyType := "selfhosted"
	if conf.OrgMultitenant() {
		tenancyType = "multitenant"
	}
	authMethod := conf.AuthMethod()
	return func(c *gin.Context) {

		if span := trace.SpanFromContext(c.Request.Context()); span != nil {
			span.SetAttributes(
				attribute.String("hoop.gateway.environment", environment),
				attribute.String("hoop.gateway.grpc-url", grpcURL),
				attribute.String("hoop.gateway.tenancy-type", tenancyType),
				attribute.String("hoop.gateway.auth-method", authMethod),
				attribute.String("hoop.gateway.platform", vs.Platform),
				attribute.String("hoop.gateway.version", vs.Version),
			)
			if ctx := storagev2.ParseContext(c); ctx != nil {
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
}

func SetSidSpanAttr(c *gin.Context, sid string) {
	if span := trace.SpanFromContext(c.Request.Context()); span != nil && sid != "" {
		span.SetAttributes(attribute.String("hoop.gateway.sid", sid))
	}
}
