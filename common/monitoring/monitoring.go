package monitoring

import (
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/pyroscope-io/client/pyroscope"
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

func StartProfiler(appName string, config ProfilerConfig) (*pyroscope.Profiler, error) {
	if config.PyroscopeAuthToken == "" || config.PyroscopeServerAddress == "" {
		return nil, nil
	}
	if config.Environment != "" {
		config.Environment = NormalizeEnvironment(config.Environment)
	}
	runtime.SetMutexProfileFraction(5)
	runtime.SetBlockProfileRate(5)

	if config.OrgName != "" {
		appName = fmt.Sprintf("%s.%s", appName, config.OrgName)
	}
	info := version.Get()
	return pyroscope.Start(pyroscope.Config{
		ApplicationName: appName,

		// replace this with the address of pyroscope server
		ServerAddress: config.PyroscopeServerAddress,

		// Logger: pyroscope.StandardLogger,
		Logger: nil,

		// optionally, if authentication is enabled, specify the API key:
		AuthToken: config.PyroscopeAuthToken,

		// you can provide static tags via a map:
		Tags: map[string]string{
			"hostname":    os.Getenv("HOSTNAME"),
			"version":     info.Version,
			"platform":    info.Platform,
			"environment": config.Environment,
		},

		ProfileTypes: []pyroscope.ProfileType{
			// these profile types are enabled by default:
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,

			// these profile types are optional:
			pyroscope.ProfileGoroutines,
			pyroscope.ProfileMutexCount,
			pyroscope.ProfileMutexDuration,
			pyroscope.ProfileBlockCount,
			pyroscope.ProfileBlockDuration,
		},
	})
}

// sentryTransport defines which transport to start, sync or async.
// a nil value defaults initalizing a sync sentry transport.
func StartSentry(sentryTransport sentry.Transport, conf SentryConfig) (bool, error) {
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
