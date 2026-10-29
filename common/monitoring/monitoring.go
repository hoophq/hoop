package monitoring

import (
	"net/url"
	"strings"

	"github.com/getsentry/sentry-go"
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

// sentryTransport defines which transport to start, sync or async.
// a nil value defaults initalizing a sync sentry transport.
func StartSentry(environment string) (bool, error) {
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
		Environment:      environment,
		Release:          version.Get().Version,
		Transport:        nil,
	})
	return err == nil, err
}
