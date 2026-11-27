package telemetry

import (
	"os"
	"sort"
	"strings"
)

// DropOTELEnv removes every OTEL_* variable from the process environment
// and returns their names, sorted. gateway.Run calls it first, before
// appconfig.Load and before any goroutine of its own.
//
// A platform can inject OTEL_* into every pod (the EKS CloudWatch
// Observability add-on does), and the OpenTelemetry SDK reads them by
// itself, some with no option to override them (OTEL_GO_X_*). The gateway's
// own settings are HOOP_OTEL_*. Only the standard names are removed.
//
// The SDK reads them lazily, when a provider, reader or exporter is built.
// Package init runs before main, so an SDK upgrade that reads OTEL_* at init
// would see them before this runs: TestAnInjectedOTELVariableChangesNothing
// is the check.
func DropOTELEnv() (names []string, err error) {
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "OTEL_") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if err := os.Unsetenv(name); err != nil {
			return names, err
		}
	}
	return names, nil
}
