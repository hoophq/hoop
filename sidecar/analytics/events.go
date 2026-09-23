package analytics

// Event names one thing the sidecar reports. The type exists so Track
// refuses a string literal: a name typed at the call site is one nobody can
// find again when the dashboard reading it goes to zero.
//
// Naming follows gateway/analytics/events.go, hoop-<noun>-<verb>, with the
// sidecar- prefix so both products' events sort together and apart. Every
// event carries the common properties New sets; the list under each name is
// what the caller adds.
type Event string

const (
	// EventFirstRun: a bare invocation served the first-run redirect page.
	// The install funnel's top. Properties: deprecated-alias, port,
	// port-fell-back, duration-seconds.
	EventFirstRun Event = "hoop-sidecar-first-run"

	// EventStarted: Run built every lane and is about to serve. Properties:
	// the boot facts (deprecated-alias, config-source, config-format,
	// control-plane-imported, file-listeners-ignored, license-state,
	// license-required, deprecations-count, detector-attached) plus the
	// config shape, see daemon.shapeProperties.
	EventStarted Event = "hoop-sidecar-started"

	// EventConfigApplied: a changed document reached the reloader, from the
	// heartbeat or the config file. Properties: config-generation, outcome
	// (applied|restart-required|refused), lanes-swapped, lanes-kept, plus
	// the config shape when applied.
	EventConfigApplied Event = "hoop-sidecar-config-applied"

	// EventUsage: counters since the previous usage event, on a ticker and
	// once at shutdown. Properties: interval-seconds, uptime-seconds,
	// connections-total, connections-active, connections-denied,
	// statements-total, statements-denied, statements-masked,
	// heartbeat-failures, by-protocol.
	EventUsage Event = "hoop-sidecar-usage"

	// EventStopped: Run is returning. Properties: reason
	// (signal|listener-failed|license-expired), uptime-seconds,
	// reloads-applied, reloads-restart-required, reloads-refused.
	EventStopped Event = "hoop-sidecar-stopped"

	// EventLicenseExpired: the license term ended under a config the free
	// tier refuses, and the process is stopping for it. Properties:
	// guardrail-rules-total, mask-rules-total, uptime-seconds.
	EventLicenseExpired Event = "hoop-sidecar-license-expired"
)
