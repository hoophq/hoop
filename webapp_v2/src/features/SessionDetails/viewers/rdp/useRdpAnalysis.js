import { useCallback, useEffect, useSyncExternalStore } from 'react'
import { sessionsService } from '@/services/sessions'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'
import { showSnackbar } from '@/utils/snackbar'
import { createAnalysisPoller } from './analysisPoller'

const poller = createAnalysisPoller({
  fetch: (id) => sessionsService.rdpDetections(id),
  retry: (id) => sessionsService.retryRdpDetections(id),
  // The worker writes the masking counts when it ends: read the session (and
  // its report) again so the data masking block shows them.
  onDone: (id) => {
    const details = useSessionDetailsStore.getState()
    if (details.sessionId === id) details.refresh()
  },
  onRetryError: (err) =>
    showSnackbar({
      level: 'error',
      text: 'Failed to retry the analysis',
      description: err?.response?.data?.message || err?.message,
    }),
})

// The PII analysis of an RDP session: { loading, analysis, detections,
// retrying, error, retry }. Pass a null id to hold nothing.
export function useRdpAnalysis(sessionId) {
  useEffect(() => (sessionId ? poller.hold(sessionId) : undefined), [sessionId])
  const subscribe = useCallback(
    (listener) => (sessionId ? poller.subscribe(sessionId, listener) : () => {}),
    [sessionId],
  )
  const state = useSyncExternalStore(subscribe, () => poller.snapshot(sessionId))
  const retry = useCallback(() => poller.retry(sessionId), [sessionId])
  return { ...state, retry }
}
