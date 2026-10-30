import { create } from 'zustand'
import { connectionsService } from '@/services/connections'
import { sessionsService } from '@/services/sessions'
import { reviewsService } from '@/services/reviews'
import { runbooksService } from '@/services/runbooks'
import { useAuthStore } from '@/stores/useAuthStore'
import { showSnackbar } from '@/utils/snackbar'
import { localTimeToUtc } from '@/utils/datetime'
import { recordingFormat } from '@/features/SessionDetails/decoders/sessionFormat'

// Above this, GET /sessions/:id does not expand the stream or the input.
export const LARGE_PAYLOAD_BYTES = 4 * 1024 * 1024

// An exec the gateway stopped waiting for (HTTP 202 after 50 s) keeps running in
// the agent: poll every 5 s for 60 tries, then every 30 s.
const WATCH_FAST_MS = 5000
const WATCH_SLOW_MS = 30000
const WATCH_FAST_TRIES = 60
// The gateway writes the outcome of a decision or an exec a moment later.
const REFETCH_DELAY_MS = 800

// The stream encoding each kind of session asks for.
export function streamEncoding(session) {
  if (session.verb === 'exec') return 'base64'
  // A live session keeps its raw wire frames: the live tail decodes the
  // history and the streamed events the same way.
  if (session.verb === 'connect' && session.status === 'open') return null
  if (session.connection_subtype === 'postgres') return 'raw-queries'
  return null
}

function errorOf(err) {
  const status = err?.response?.status
  const message =
    err?.response?.data?.message ||
    (status === 404 && 'This session does not exist.') ||
    (status === 403 && 'You are not allowed to see this session.') ||
    err?.message ||
    'Failed to load the session.'
  return { status, message }
}

const messageOf = (err) => err?.response?.data?.message || err?.message

// labels.runbookParameters holds JSON; a broken value counts as no parameters.
export function parseParameters(json) {
  if (!json) return {}
  try {
    const value = JSON.parse(json)
    return value && typeof value === 'object' && !Array.isArray(value) ? value : {}
  } catch {
    return {}
  }
}

const initial = {
  opened: false,
  sessionId: null,
  // What the opener knew (a list row, a workflow step, or { id, verb } from the
  // ClojureScript bridge), then the session as the gateway returns it.
  session: null,
  status: 'idle', // idle | loading | ready | error
  error: null,
  largeOutput: false,
  largeInput: false,
  report: null,
  // A decision modal (reject, time window) is open over the details: Escape
  // and an outside click close it, not the details.
  stacked: false,
  // The groups that may force-approve this review, as the gateway decides.
  forceGroups: [],
  onClose: null,
  // Set when a route shows the details: another session is shown by
  // navigating, so the URL follows it.
  onNavigate: null,
  requestId: 0,
  // Per session id: { status: 'executing' | 'running' | 'done', attempts }.
  executions: {},
  deciding: false,
  killing: false,
  rerunning: false,
  // The session a re-run of this one created: the header links it (NB05).
  rerunId: null,
}

// Bumped after the details change a session (a decision, an execution, a kill,
// a re-run): a list on screen reads it and fetches again.
let revision = 0

let watchTimer = null

function stopWatch() {
  clearTimeout(watchTimer)
  watchTimer = null
}

export const useSessionDetailsStore = create((set, get) => ({
  ...initial,
  revision,

  // A list on screen fetches again when this changes.
  bumpRevision: () => set({ revision: ++revision }),

  // One modal for the whole app: a new open replaces the session it shows.
  open: (id, { prefill = null, onClose = null, onNavigate = null } = {}) => {
    stopWatch()
    set({
      ...initial,
      revision,
      opened: true,
      sessionId: id,
      session: prefill ? { ...prefill, id } : { id },
      status: 'loading',
      onClose,
      onNavigate,
      requestId: get().requestId,
    })
    get().load(id)
  },

  close: () => {
    const { onClose, requestId } = get()
    stopWatch()
    set({ ...initial, revision, requestId: requestId + 1 })
    onClose?.()
  },

  // Reads the session again without the skeleton.
  refresh: () => {
    const { sessionId } = get()
    if (sessionId) return get().load(sessionId, { keep: true })
  },

  // Shows another session in the same modal (a re-run, a linked session).
  showSession: (id, prefill = null) => {
    const { onNavigate, onClose } = get()
    if (onNavigate) onNavigate(id)
    else get().open(id, { prefill, onClose })
  },

  setStacked: (stacked) => set({ stacked }),

  // Merges what a viewer learned (the live tail sees the session end).
  patchSession: (patch) => set((s) => (s.session ? { session: { ...s.session, ...patch } } : {})),

  load: async (id, { keep = false } = {}) => {
    const requestId = get().requestId + 1
    set(keep ? { requestId } : { requestId, status: 'loading', error: null })
    const current = () => get().requestId === requestId && get().sessionId === id
    try {
      // The sizes decide what the second read expands.
      const { data: base } = await sessionsService.get(id)
      if (!current()) return
      const largeOutput = (base.event_size ?? 0) > LARGE_PAYLOAD_BYTES
      const largeInput = (base.script_size ?? 0) > LARGE_PAYLOAD_BYTES
      let session = base
      if (!(largeOutput && largeInput)) {
        // A finished RDP session replays from /rdp-frames: its stream is never shown.
        const rdpReplay = base.verb === 'connect' && base.status !== 'open' && recordingFormat(base) === 'rdp'
        const skipStream = largeOutput || rdpReplay
        const expand = [!skipStream && 'event_stream', !largeInput && 'session_input'].filter(Boolean)
        const encoding = skipStream ? null : streamEncoding(base)
        const { data } = await sessionsService.get(id, {
          expand: expand.join(','),
          ...(encoding ? { event_stream: encoding } : {}),
        })
        if (!current()) return
        session = { ...base, ...data }
      }
      set({ session, status: 'ready', error: null, largeOutput, largeInput })
      if (largeOutput && largeInput) return
      get().loadReport(session, requestId)
      if (session.review?.id) {
        get().loadReviewSource(session, requestId)
        get().loadForceGroups(session, requestId)
      }
      get().resumeWatch(session)
    } catch (err) {
      if (!current()) return
      set({ status: 'error', error: errorOf(err) })
    }
  },

  loadReport: async (session, requestId) => {
    if (!session.start_date) return
    try {
      const { data } = await sessionsService.report(session.id, session.start_date.split('T')[0])
      if (get().requestId === requestId) set({ report: data })
    } catch {
      // The masking card falls back to the session metrics.
    }
  },

  // The review inside a session says nothing about where a sidecar review came
  // from; the review API reports its listener_name and sidecar_id.
  loadReviewSource: async (session, requestId) => {
    try {
      const { data } = await reviewsService.get(session.review.id)
      if (get().requestId !== requestId || !data?.listener_name) return
      set((s) => ({
        session: {
          ...s.session,
          review: { ...s.session.review, listener_name: data.listener_name, sidecar_id: data.sidecar_id },
        },
      }))
    } catch {
      // Not a sidecar review, or not visible to this user: nothing to add.
    }
  },

  // The gateway takes the force groups of the review only when an access
  // request rule filed it, and those of the connection otherwise.
  loadForceGroups: async (session, requestId) => {
    const { review } = session
    if (review.status !== 'PENDING') return
    if (review.access_request_rule_name && review.force_approval_groups) {
      set({ forceGroups: review.force_approval_groups })
      return
    }
    if (!session.connection) return
    try {
      const connection = await connectionsService.getConnection(session.connection)
      if (get().requestId === requestId) set({ forceGroups: connection?.force_approve_groups || [] })
    } catch {
      // Without the connection nobody may force-approve from here.
    }
  },

  // ── Review ────────────────────────────────────────────────────────────────
  // decision: { status: 'APPROVED' | 'REJECTED', rejectionReason?, forceReview?,
  // timeWindow?: { start, end } in local HH:mm }.
  decide: async ({ status, rejectionReason, forceReview, timeWindow }) => {
    const { session } = get()
    if (!session?.review?.id) return
    const body = { status }
    if (timeWindow) {
      body.time_window = {
        type: 'time_range',
        configuration: { start_time: localTimeToUtc(timeWindow.start), end_time: localTimeToUtc(timeWindow.end) },
      }
    }
    if (forceReview) body.force_review = true
    if (rejectionReason?.trim()) body.rejection_reason = rejectionReason.trim()
    const rejected = status === 'REJECTED'
    set({ deciding: true })
    try {
      await reviewsService.update(session.review.id, body)
      showSnackbar({ level: 'success', text: rejected ? 'Your rejection was recorded' : 'Your approval was recorded' })
      setTimeout(() => {
        get().refresh()
        get().bumpRevision()
      }, REFETCH_DELAY_MS)
      return true
    } catch (err) {
      showSnackbar({ level: 'error', text: rejected ? 'Failed to reject' : 'Failed to approve', description: messageOf(err) })
      return false
    } finally {
      set({ deciding: false })
    }
  },

  // ── Execute an approved exec ──────────────────────────────────────────────
  execute: async () => {
    const { session, executions } = get()
    const id = session?.id
    // A double click never issues a second POST.
    if (!id || ['executing', 'running'].includes(executions[id]?.status)) return
    get().setExecution(id, { status: 'executing' })
    try {
      const { data } = await sessionsService.exec(id)
      if (data?.output_status === 'running') {
        get().setExecution(id, { status: 'running', attempts: 0 })
        get().scheduleWatch(id)
      } else {
        get().setExecution(id, { status: 'done' })
        setTimeout(() => {
          get().refresh()
          get().bumpRevision()
        }, REFETCH_DELAY_MS)
      }
    } catch (err) {
      // Read the session again so the Execute gate follows the server.
      get().setExecution(id, null)
      showSnackbar({ level: 'error', text: 'Failed to execute script', description: messageOf(err) })
      get().refresh()
    }
  },

  setExecution: (id, value) =>
    set((s) => {
      const executions = { ...s.executions }
      if (value) executions[id] = value
      else delete executions[id]
      return { executions }
    }),

  // A reviewed exec runs past the API wait: the review reads PROCESSING while it
  // runs and UNKNOWN after the gateway stopped waiting. Resume the watch after a
  // reload, when nothing local tracks it.
  resumeWatch: (session) => {
    const running = ['PROCESSING', 'UNKNOWN'].includes(session.review?.status)
    if (session.verb !== 'exec' || !running || session.status === 'done') return
    if (get().executions[session.id]) return
    get().setExecution(session.id, { status: 'running', attempts: 0 })
    get().scheduleWatch(session.id)
  },

  scheduleWatch: (id) => {
    stopWatch()
    const attempts = get().executions[id]?.attempts ?? 0
    watchTimer = setTimeout(() => get().watch(id), attempts < WATCH_FAST_TRIES ? WATCH_FAST_MS : WATCH_SLOW_MS)
  },

  watch: async (id) => {
    const execution = get().executions[id]
    if (execution?.status !== 'running' || get().sessionId !== id) return
    get().setExecution(id, { ...execution, attempts: (execution.attempts ?? 0) + 1 })
    try {
      const { data } = await sessionsService.get(id)
      if (data?.status === 'done') {
        get().setExecution(id, { status: 'done' })
        get().refresh()
        get().bumpRevision()
        return
      }
    } catch {
      // A transient error: keep polling.
    }
    get().scheduleWatch(id)
  },

  // ── Kill a running exec ───────────────────────────────────────────────────
  kill: async () => {
    const { session } = get()
    if (!session?.id) return
    set({ killing: true })
    try {
      await sessionsService.kill(session.id)
      showSnackbar({ level: 'success', text: 'Session killed successfully' })
      get().refresh()
      get().bumpRevision()
    } catch (err) {
      showSnackbar({ level: 'error', text: 'Failed to kill session', description: messageOf(err) })
    } finally {
      set({ killing: false })
    }
  },

  // ── Re-run an exec ────────────────────────────────────────────────────────
  // extra: { jira_fields, cmdb_fields } from the Jira template gate.
  // A runbook session runs its runbook again on its own resource, with its own
  // parameters: nothing from the runner page (parallel selection, metadata).
  rerun: async (extra = {}) => {
    const { session } = get()
    if (!session?.id) return
    const correlation = session.correlation_id?.trim() ? { correlation_id: session.correlation_id } : {}
    const labels = session.labels || {}
    set({ rerunning: true })
    try {
      const { data } = labels.runbookFile
        ? await runbooksService.exec({
            file_name: labels.runbookFile,
            connection_name: session.connection,
            repository: labels.runbookRepository,
            parameters: parseParameters(labels.runbookParameters),
            ...correlation,
            ...extra,
          })
        : await sessionsService.create({
            script: session.script?.data,
            labels: { 're-run-from': session.id },
            connection: session.connection,
            ...correlation,
            ...extra,
          })
      get().showRerun(data?.session_id, session.id)
    } catch (err) {
      // The gateway answers an error with the session it created when the run
      // itself failed: show that session, do not post again.
      const sessionId = err?.response?.data?.session_id
      if (sessionId) get().showRerun(sessionId, session.id)
      else showSnackbar({ level: 'error', text: messageOf(err) || 'Failed to re-run the session' })
    } finally {
      set({ rerunning: false })
    }
  },

  // The original session stays open, with a note that links the new one.
  showRerun: (sessionId, originId) => {
    if (sessionId && get().sessionId === originId) set({ rerunId: sessionId })
    setTimeout(() => get().bumpRevision(), REFETCH_DELAY_MS)
  },

  reset: () => {
    stopWatch()
    set({ ...initial, revision, requestId: get().requestId + 1 })
  },
}))

useAuthStore.subscribe((state, prev) => {
  if (prev.isAuthenticated && !state.isAuthenticated) useSessionDetailsStore.getState().reset()
})
