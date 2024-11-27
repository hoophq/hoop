package monitoring

import (
	"net/url"
	"os"
	"strings"

	"github.com/getsentry/sentry-go"
	"github.com/honeycombio/honeycomb-opentelemetry-go"
	"github.com/honeycombio/otel-config-go/otelconfig"
	"github.com/hoophq/hoop/common/version"
)

type TransportConfig struct {
	Sentry   SentryConfig
	Profiler ProfilerConfig
}

type SentryConfig struct {
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
func StartSentry() (bool, error) {
	if sentryDSN == "" {
		return false, nil
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:   sentryDSN,
		Debug: false,
		// Set TracesSampleRate to 1.0 to capture 100%
		// of transactions for performance monitoring.
		// We recommend adjusting this value in production,
		TracesSampleRate: 1.0,
		Environment:      "", // TODO
		Release:          version.Get().Version,
		Transport:        nil,
	})
	return err == nil, err
}

type ShutdownFn func()

func NewOpenTracing(apiURL string) (ShutdownFn, error) {
	if isLocalEnvironment(apiURL) {
		return func() {}, nil
	}
	// Enable multi-span attributes
	bsp := honeycomb.NewBaggageSpanProcessor()

	hcApiKey := honeycombApiKey
	if hcApiKey == "" {
		// used only for development purposes
		hcApiKey = os.Getenv("HONEYCOMB_API_KEY")
	}
	// Use the Honeycomb distro to set up the OpenTelemetry SDK
	return otelconfig.ConfigureOpenTelemetry(
		otelconfig.WithSpanProcessor(bsp),
		otelconfig.WithServiceName("hoopdev"),
		otelconfig.WithServiceVersion(version.Get().Version),
		otelconfig.WithExporterEndpoint("https://api.honeycomb.io:443"),
		// otelconfig.WithLogLevel("debug"),
		otelconfig.WithHeaders(map[string]string{
			"x-honeycomb-team": hcApiKey,
		}),
	)
}
