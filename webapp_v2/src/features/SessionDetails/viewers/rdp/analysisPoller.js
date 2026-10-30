import { ACTIVE_ANALYSIS_STATUSES, normalizeAnalysis } from './rdpLogic'

export const ANALYSIS_POLL_MS = 5000

const INITIAL = Object.freeze({
  loading: true,
  analysis: normalizeAnalysis(null),
  detections: [],
  retrying: false,
  error: null,
})

// A 4xx will answer the same way next time; anything else may be transient.
const isPermanent = (err) => {
  const status = err?.response?.status
  return status >= 400 && status < 500
}

const messageOf = (err) => err?.response?.data?.message || err?.message || 'Request failed.'

/**
 * One RDP analysis poll per session, shared by every view that shows it (the
 * player's legend and the data masking title). It reads the detections when
 * the first view holds it, polls every 5 s while the job is pending or
 * running, and stops on done, failed, a 4xx, or when the last view lets go.
 *
 * deps: { fetch(id), retry(id), onDone(id), onRetryError(err) }; the timers are
 * injectable for tests.
 */
export function createAnalysisPoller({
  fetch,
  retry,
  onDone,
  onRetryError,
  setTimer = setTimeout,
  clearTimer = clearTimeout,
}) {
  const entries = new Map()

  const update = (entry, patch) => {
    entry.snapshot = { ...entry.snapshot, ...patch }
    entry.listeners.forEach((listener) => listener())
  }

  const schedule = (id) => {
    const entry = entries.get(id)
    if (!entry) return
    clearTimer(entry.timer)
    entry.timer = setTimer(() => poll(id), ANALYSIS_POLL_MS)
  }

  const apply = (id, response) => {
    const entry = entries.get(id)
    if (!entry) return
    const analysis = normalizeAnalysis(response)
    update(entry, { loading: false, error: null, analysis, detections: response?.detections || [] })
    const active = ACTIVE_ANALYSIS_STATUSES.has(analysis.status)
    // Only a job seen running and then done refreshes the session: opening an
    // analysis that was already done reads nothing twice.
    if (entry.wasActive && analysis.status === 'done') onDone?.(id)
    entry.wasActive = active
    if (active) schedule(id)
  }

  const poll = async (id) => {
    if (!entries.has(id)) return
    try {
      const { data } = await fetch(id)
      apply(id, data)
    } catch (err) {
      const entry = entries.get(id)
      if (!entry) return
      update(entry, { loading: false, error: messageOf(err) })
      if (!isPermanent(err)) schedule(id)
    }
  }

  const entryFor = (id) => {
    let entry = entries.get(id)
    if (!entry) {
      entry = { snapshot: INITIAL, listeners: new Set(), holders: 0, timer: null, wasActive: false }
      entries.set(id, entry)
    }
    return entry
  }

  return {
    // A view starts holding the poll; the first one reads the detections.
    hold(id) {
      const entry = entryFor(id)
      entry.holders += 1
      if (entry.holders === 1) poll(id)
      return () => {
        entry.holders -= 1
        if (entry.holders > 0) return
        clearTimer(entry.timer)
        entries.delete(id)
      }
    },

    subscribe(id, listener) {
      const entry = entryFor(id)
      entry.listeners.add(listener)
      return () => entry.listeners.delete(listener)
    },

    snapshot(id) {
      return entries.get(id)?.snapshot || INITIAL
    },

    // Requeue a failed job, or start one that never ran. The gateway answers
    // with the detections payload, status pending.
    async retry(id) {
      const entry = entries.get(id)
      if (!entry || entry.snapshot.retrying) return
      update(entry, { retrying: true })
      try {
        const { data } = await retry(id)
        if (entries.get(id) !== entry) return
        update(entry, { retrying: false })
        apply(id, data)
      } catch (err) {
        if (entries.get(id) === entry) update(entry, { retrying: false })
        onRetryError?.(err)
      }
    },
  }
}
