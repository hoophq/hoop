/* global __SENTRY_DSN__, __SENTRY_SAMPLE_RATE__ */

let started = false

// Same gate as CLJS :initialize-monitoring (events/tracking.cljs): Sentry runs
// only while analytics tracking is NOT enabled.
export function sentryEnabled({ dsn, sampleRate, analyticsTracking }) {
  return Boolean(dsn && sampleRate) && !analyticsTracking
}

export function initSentry({ analyticsTracking, release }) {
  const dsn = __SENTRY_DSN__
  const sampleRate = __SENTRY_SAMPLE_RATE__
  if (started || !sentryEnabled({ dsn, sampleRate, analyticsTracking })) return
  started = true
  import('@sentry/browser')
    .then((Sentry) =>
      Sentry.init({
        dsn,
        release: release || undefined,
        sampleRate: Number(sampleRate),
        integrations: [Sentry.browserTracingIntegration()],
      }),
    )
    .catch((err) => console.error('Failed to initialize Sentry:', err))
}
