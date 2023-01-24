package analytics

import (
	"fmt"
	"github.com/getsentry/sentry-go"
)

func InitSentry() {
	if err := sentry.Init(sentry.ClientOptions{
		Dsn:           "https://7c3bcdf7772943b9b70bcf69b07408ae@o4504559799566336.ingest.sentry.io/4504559805923328",
		EnableTracing: true,
		// Set TracesSampleRate to 1.0 to capture 100%
		// of transactions for performance monitoring.
		// We recommend adjusting this value in production,
		TracesSampleRate: 1.0,
	}); err != nil {
		fmt.Printf("Sentry initialization failed: %v\n", err)
	}
}
