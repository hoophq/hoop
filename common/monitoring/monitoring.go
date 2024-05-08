package monitoring

import (
	"net/url"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/honeycombio/honeycomb-opentelemetry-go"
	"github.com/honeycombio/otel-config-go/otelconfig"
	"github.com/runopsio/hoop/common/version"
	"github.com/spf13/cobra"
)

type TransportConfig struct {
	Sentry   SentryConfig
	Profiler ProfilerConfig
}

type SentryConfig struct {
	DSN         string
	OrgName     string
	Environment string
}

type ProfilerConfig struct {
	PyroscopeServerAddress string
	PyroscopeAuthToken     string
	OrgName                string
	Environment            string
}

func NormalizeEnvironment(apiURL string) string {
	if u, _ := url.Parse(apiURL); u != nil {
		return u.Hostname()
	}
	environment := strings.TrimPrefix(apiURL, "http://")
	return strings.TrimPrefix(environment, "https://")
}

func isLocalEnvironment(environment string) bool {
	return environment == "localhost" ||
		environment == "127.0.0.1" ||
		strings.HasPrefix(environment, "http://localhost") ||
		strings.HasPrefix(environment, "http://127.0.0.1")
}

// sentryTransport defines which transport to start, sync or async.
// a nil value defaults initalizing a sync sentry transport.
func StartSentry(sentryTransport sentry.Transport, conf SentryConfig) (bool, error) {
	if isLocalEnvironment(conf.Environment) {
		return false, nil
	}
	if conf.DSN == "" {
		return false, nil
	}
	if conf.Environment != "" {
		conf.Environment = NormalizeEnvironment(conf.Environment)
	}
	sentry.ConfigureScope(func(scope *sentry.Scope) {
		if conf.OrgName != "" {
			scope.SetTag("orgname", conf.OrgName)
		}
	})
	err := sentry.Init(sentry.ClientOptions{
		Dsn:   conf.DSN,
		Debug: false,
		// Set TracesSampleRate to 1.0 to capture 100%
		// of transactions for performance monitoring.
		// We recommend adjusting this value in production,
		TracesSampleRate: 1.0,
		Environment:      conf.Environment,
		Release:          version.Get().Version,
		Transport:        sentryTransport,
	})
	return err == nil, err
}

func SentryPreRun(cmd *cobra.Command, args []string) {
	sentrySyncTransport := sentry.NewHTTPSyncTransport()
	sentrySyncTransport.Timeout = time.Second * 3
	StartSentry(sentrySyncTransport, SentryConfig{
		// hoop-client
		DSN: "https://7e38ad7875464bf2a475486c325a73b2@o4504559799566336.ingest.sentry.io/4504576866385920"})
}

type ShutdownFn func()

func NewOpenTracing(apiURL, apiKey string) (ShutdownFn, error) {
	if isLocalEnvironment(apiURL) {
		return func() {}, nil
	}
	// Enable multi-span attributes
	bsp := honeycomb.NewBaggageSpanProcessor()

	// Use the Honeycomb distro to set up the OpenTelemetry SDK
	return otelconfig.ConfigureOpenTelemetry(
		otelconfig.WithSpanProcessor(bsp),
		otelconfig.WithServiceName("hoopdev"),
		otelconfig.WithExporterEndpoint("https://api.honeycomb.io:443"),
		otelconfig.WithHeaders(map[string]string{
			"x-honeycomb-team": apiKey,
		}),
	)
}
