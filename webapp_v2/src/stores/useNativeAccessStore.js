import { create } from 'zustand'
import { connectionCredentialsService } from '@/services/connectionCredentials'
import { connectionsService } from '@/services/connections'
import { useNativeConnectionsStore } from '@/stores/useNativeConnectionsStore'
import { showSnackbar } from '@/utils/snackbar'
import { subscribeTick } from '@/utils/tick'
import {
  ERROR_MESSAGES,
  REQUEST_FAILED_FALLBACK,
  REVIEW_POLL_MS,
} from '@/features/NativeConnections/constants'
import { errorMessage, isSessionValid } from '@/features/NativeConnections/helpers'

/**
 * Native client access — the React port of
 * webapp/src/webapp/connections/native_client_access/events.cljs.
 *
 * Two deliberate departures from the CLJS original:
 *
 * 1. No localStorage. The CLJS version persisted whole credential responses
 *    (plaintext secrets included) under "hoop-native-client-access" and restored
 *    them on every panel re-render. Here the server is authoritative:
 *    GET /connection-credentials lists live credentials without secrets, and the
 *    secret is fetched per connection only when the user opens a row. A lost
 *    cache costs one request, not a session — the gateway re-issues the same
 *    secret for a given (user, connection) pair.
 *
 * 2. Expiry lives here, not in the view. The CLJS timer fired its completion
 *    callback from every mounted instance, so the "session has expired" toast
 *    fired twice whenever the modal and the draggable card were both open.
 */

// Per-connection lifecycle. `unavailable` covers both an offline agent and a
// failed connection lookup; `pendingReview` means the POST answered 202.
export const FLOW_STATUS = {
  IDLE: 'idle',
  CHECKING: 'checking',
  CONFIGURING: 'configuring',
  REQUESTING: 'requesting',
  READY: 'ready',
  PENDING_REVIEW: 'pendingReview',
  UNAVAILABLE: 'unavailable',
}

const setIn = (map, key, value) => ({ ...map, [key]: value })
const removeFrom = (map, key) => {
  const next = { ...map }
  delete next[key]
  return next
}

let unsubscribeTick = null
let reviewPollId = null
let reviewPollInFlight = false

export const useNativeAccessStore = create((set, get) => ({
  // connectionName -> ConnectionCredentialsListItem (no secrets)
  activeByName: {},
  // connectionName -> full credential response (secrets; fetched on demand)
  credentialsByName: {},
  // connectionName -> the connection payload, for jit/status/reviewers
  connectionByName: {},
  // connectionName -> { sessionId, reviewId }
  reviewByName: {},
  // connectionName -> FLOW_STATUS
  statusByName: {},
  // connectionName -> user-facing message for the unavailable state
  errorByName: {},

  // The connection whose "ask for access" dialog is open, or null. The design
  // puts the duration step in a dialog over the drawer rather than inside the
  // row, so the drawer stays visible behind it and the row it belongs to keeps
  // whatever state it already had.
  requestFor: null,

  activeLoading: false,
  activeLoaded: false,

  statusOf: (name) => get().statusByName[name] || FLOW_STATUS.IDLE,

  // ── active credentials ────────────────────────────────────────────────

  loadActive: async () => {
    set({ activeLoading: true })
    try {
      const { items = [] } = await connectionCredentialsService.listActive()
      const activeByName = {}
      items.forEach((item) => {
        activeByName[item.connection_name] = item
      })
      set({ activeByName, activeLoaded: true })
      get().syncExpiryWatcher()
    } catch (error) {
      // Non-fatal: the drawer still lists roles, just without live-session
      // decoration. Surfacing a toast on every drawer open would be noise.
      console.error('Failed to load active native credentials', error)
    } finally {
      set({ activeLoading: false })
    }
  },

  /**
   * Keeps a single tick subscription alive while at least one active credential
   * has an expiry, and drops sessions the moment they run out. Runs whether or
   * not the drawer is open, so a countdown that hits zero always cleans up.
   */
  syncExpiryWatcher: () => {
    const hasBounded = Object.values(get().activeByName).some((item) => item.expire_at)
    if (hasBounded && !unsubscribeTick) {
      unsubscribeTick = subscribeTick(() => {
        const { activeByName } = get()
        const expired = Object.values(activeByName).filter((item) => !isSessionValid(item))
        if (!expired.length) return
        expired.forEach((item) => get().clearSession(item.connection_name))
        // One toast per sweep, not per session: after the machine wakes from
        // sleep the interval catches up and several sessions can lapse in the
        // same tick.
        showSnackbar({
          level: 'info',
          text:
            expired.length === 1
              ? 'Native client access session has expired.'
              : `${expired.length} native client access sessions have expired.`,
        })
      })
    } else if (!hasBounded && unsubscribeTick) {
      unsubscribeTick()
      unsubscribeTick = null
    }
  },

  // ── the flow ──────────────────────────────────────────────────────────

  openRequestDialog: (connectionName) => set({ requestFor: connectionName }),
  closeRequestDialog: () => set({ requestFor: null }),

  /**
   * The row's action button — "Connect", or "Ask access" when the role is
   * gated on review.
   *
   * Connecting is driven by this button, never by expanding the row. Expanding
   * used to start the flow, which meant a review-gated role fired a request the
   * moment it was opened, before the user had chosen anything.
   *
   * Look the connection up first: the list payload carries `reviewers` but not
   * `jit_access_duration_sec` (ToOpenApi populates it only on Get), so a JIT
   * window is invisible until we fetch. Then either surface the unavailable
   * state, show the credential that already exists, open the duration dialog,
   * or — for a role with no review at all — connect in one click.
   */
  beginConnect: async (connectionName) => {
    set((s) => ({
      statusByName: setIn(s.statusByName, connectionName, FLOW_STATUS.CHECKING),
      errorByName: removeFrom(s.errorByName, connectionName),
    }))

    const unavailable = (message) => {
      set((s) => ({
        statusByName: setIn(s.statusByName, connectionName, FLOW_STATUS.UNAVAILABLE),
        errorByName: setIn(s.errorByName, connectionName, message),
      }))
      // Open the row so the reason is visible — the button is gone at this
      // point and the row would otherwise just stop responding.
      useNativeConnectionsStore.getState().setExpanded(connectionName)
    }

    let connection
    try {
      connection = await connectionsService.getConnection(connectionName)
    } catch {
      unavailable(ERROR_MESSAGES.agentOffline)
      return
    }

    set((s) => ({ connectionByName: setIn(s.connectionByName, connectionName, connection) }))

    if (connection.status !== 'online') {
      unavailable(ERROR_MESSAGES.agentOffline)
      return
    }

    // A live credential already exists — go straight to showing it.
    const active = get().activeByName[connectionName]
    if (isSessionValid(active)) {
      await get().fetchCredentials(connectionName)
      return
    }

    const requiresReview =
      connection.jit_access_duration_sec != null || (connection.reviewers?.length ?? 0) > 0

    if (requiresReview) {
      set((s) => ({
        statusByName: removeFrom(s.statusByName, connectionName),
        requestFor: connectionName,
      }))
      return
    }

    // No review and no session: no dialog to show, so connect straight away
    // with a persistent credential — the CLJS skip-configure path.
    await get().requestAccess(connectionName, null)
  },

  /**
   * Called when a row is expanded. Only loads the secret for a session that
   * already exists; it never creates one. Rows with nothing to show are not
   * expandable, so this is a no-op for them.
   */
  resumeIfActive: async (connectionName) => {
    if (get().credentialsByName[connectionName]) return
    if (!isSessionValid(get().activeByName[connectionName])) return
    await get().fetchCredentials(connectionName)
  },

  /**
   * "Connect natively to X" from outside the drawer — the command palette, the
   * sidebar ConfigStatus and the ClojureScript bridge. Opens the drawer and
   * runs the same flow the row button runs.
   */
  openAndConnect: (connectionName) => {
    useNativeConnectionsStore.getState().open()
    return get().beginConnect(connectionName)
  },

  /**
   * POST /connections/{name}/credentials.
   *
   * `accessDurationSec` is null for a persistent credential. Review-required
   * connections must pass a real window — the gateway 400s without one.
   */
  requestAccess: async (connectionName, accessDurationSec) => {
    set((s) => ({
      statusByName: setIn(s.statusByName, connectionName, FLOW_STATUS.REQUESTING),
      errorByName: removeFrom(s.errorByName, connectionName),
    }))

    try {
      const { status, data } = await connectionCredentialsService.create(
        connectionName,
        accessDurationSec
      )

      if (status === 202 || data.has_review) {
        set((s) => ({
          statusByName: setIn(s.statusByName, connectionName, FLOW_STATUS.PENDING_REVIEW),
          reviewByName: setIn(s.reviewByName, connectionName, {
            sessionId: data.session_id,
            reviewId: data.review_id,
            // Kept so the pending row can re-issue the same request once a
            // reviewer approves, without asking for the duration again.
            accessDurationSec,
          }),
          requestFor: null,
        }))
        useNativeConnectionsStore.getState().setExpanded(connectionName)
        // Start watching straight away: the review exists but the credential
        // does not, and only a later resume will create it.
        get().syncReviewWatcher()
        showSnackbar({ level: 'info', text: 'This connection requires review approval' })
        return
      }

      set({ requestFor: null })
      get().storeCredentials(data)
      showSnackbar({ level: 'success', text: 'Native client access granted successfully!' })
    } catch (error) {
      const message = errorMessage(error, REQUEST_FAILED_FALLBACK)
      set((s) => ({
        statusByName: setIn(s.statusByName, connectionName, FLOW_STATUS.UNAVAILABLE),
        errorByName: setIn(s.errorByName, connectionName, message),
        requestFor: null,
      }))
      useNativeConnectionsStore.getState().setExpanded(connectionName)
      showSnackbar({ level: 'error', text: message })
    }
  },

  /**
   * POST /connections/{name}/credentials/{sessionId} after a reviewer approves.
   * Still answers 202 while the review is pending — the CLJS version treated
   * that as success and cached a session under the key "undefined".
   *
   * `silent` is for the poller: it suppresses the "still waiting" toast and the
   * intermediate REQUESTING state, so a row that is being watched does not
   * flicker or nag once every interval. Success and failure always speak up.
   */
  resumeAfterReview: async (connectionName, sessionId, accessDurationSec, { silent } = {}) => {
    if (!silent) {
      set((s) => ({
        statusByName: setIn(s.statusByName, connectionName, FLOW_STATUS.REQUESTING),
      }))
    }
    try {
      const { status, data } = await connectionCredentialsService.resume(
        connectionName,
        sessionId,
        accessDurationSec
      )
      if (status === 202 || !data.connection_credentials) {
        set((s) => ({
          statusByName: setIn(s.statusByName, connectionName, FLOW_STATUS.PENDING_REVIEW),
        }))
        if (!silent) {
          showSnackbar({
            level: 'info',
            text: 'This connection is still waiting for review approval',
          })
        }
        return
      }
      get().storeCredentials(data)
      showSnackbar({ level: 'success', text: 'Credentials obtained successfully!' })
    } catch (error) {
      const message = errorMessage(error, 'Failed to obtain credentials')
      set((s) => ({
        statusByName: setIn(s.statusByName, connectionName, FLOW_STATUS.UNAVAILABLE),
        errorByName: setIn(s.errorByName, connectionName, message),
      }))
      showSnackbar({ level: 'error', text: message })
    } finally {
      get().syncReviewWatcher()
    }
  },

  /**
   * Watches every row that is waiting on a review and finishes the flow the
   * moment it is approved.
   *
   * A review-gated connection always ends in a bounded session, so this is what
   * makes the countdown appear at all: the 202 branch creates the review but no
   * credential, and the credential only exists once someone calls resume again.
   * Without this the row sat on "Pending review" forever and the user had to
   * know to come back and press a button.
   *
   * Polls resume rather than GET /reviews/{id} for two reasons: resume already
   * documents "202 while pending, 201 with credentials once approved", so an
   * approval is picked up in one round trip instead of two; and the reviews
   * endpoint is wrapped in TrackRequest(EventFetchReviews), which a poller would
   * quietly inflate into a meaningless number.
   */
  syncReviewWatcher: () => {
    const pending = Object.values(get().statusByName).some(
      (status) => status === FLOW_STATUS.PENDING_REVIEW
    )
    if (pending && !reviewPollId) {
      reviewPollId = setInterval(() => get().checkPendingReviews(), REVIEW_POLL_MS)
    } else if (!pending && reviewPollId) {
      clearInterval(reviewPollId)
      reviewPollId = null
    }
  },

  checkPendingReviews: async () => {
    // A slow round trip must not stack up behind the interval.
    if (reviewPollInFlight) return
    reviewPollInFlight = true
    try {
      const { statusByName, reviewByName } = get()
      const pending = Object.keys(statusByName).filter(
        (name) => statusByName[name] === FLOW_STATUS.PENDING_REVIEW
      )
      for (const name of pending) {
        const review = reviewByName[name]
        if (!review?.sessionId) continue
        await get().resumeAfterReview(name, review.sessionId, review.accessDurationSec, {
          silent: true,
        })
      }
    } finally {
      reviewPollInFlight = false
    }
  },

  /** Loads the secret for a connection that already has a live credential. */
  fetchCredentials: async (connectionName) => {
    set((s) => ({
      statusByName: setIn(s.statusByName, connectionName, FLOW_STATUS.REQUESTING),
    }))
    try {
      const data = await connectionCredentialsService.get(connectionName)
      get().storeCredentials(data)
    } catch (error) {
      // 404 means the credential was revoked or expired server-side while we
      // were showing it as active. Drop it and fall back to the idle state so
      // the user can just connect again.
      if (error?.response?.status === 404) {
        get().clearSession(connectionName)
        set((s) => ({ statusByName: setIn(s.statusByName, connectionName, FLOW_STATUS.IDLE) }))
        return
      }
      const message = errorMessage(error, 'Failed to load credentials')
      set((s) => ({
        statusByName: setIn(s.statusByName, connectionName, FLOW_STATUS.UNAVAILABLE),
        errorByName: setIn(s.errorByName, connectionName, message),
      }))
    }
  },

  /**
   * Also opens the row. The design asks the user to "come back to this view
   * with this item open" after connecting, and an established session is the
   * only thing the panel has to show.
   */
  storeCredentials: (data) => {
    const name = data.connection_name
    useNativeConnectionsStore.getState().setExpanded(name)
    set((s) => ({
      credentialsByName: setIn(s.credentialsByName, name, data),
      activeByName: setIn(s.activeByName, name, {
        id: data.id,
        connection_name: name,
        connection_type: data.connection_type,
        connection_subtype: data.connection_subtype,
        session_id: data.session_id,
        expire_at: data.expire_at,
        created_at: data.created_at,
      }),
      statusByName: setIn(s.statusByName, name, FLOW_STATUS.READY),
      reviewByName: removeFrom(s.reviewByName, name),
      errorByName: removeFrom(s.errorByName, name),
    }))
    get().syncExpiryWatcher()
    // This row is no longer pending, so the poller may have nothing left to do.
    get().syncReviewWatcher()
  },

  // ── teardown ──────────────────────────────────────────────────────────

  /**
   * Ends the audit session and tears down live proxy connections while keeping
   * the token, so reconnecting returns the same secret.
   */
  disconnect: async (connectionName, credentialId) => {
    if (!credentialId) {
      get().clearSession(connectionName)
      return
    }
    try {
      await connectionCredentialsService.close(connectionName, credentialId)
      get().clearSession(connectionName)
      showSnackbar({ level: 'success', text: 'Connection disconnected successfully.' })
    } catch (error) {
      showSnackbar({ level: 'error', text: errorMessage(error, 'Failed to disconnect') })
    }
  },

  /** Invalidates the token itself; the next request mints a new one. */
  revoke: async (connectionName, credentialId) => {
    if (!credentialId) {
      get().clearSession(connectionName)
      return
    }
    try {
      await connectionCredentialsService.revoke(connectionName, credentialId)
      get().clearSession(connectionName)
      showSnackbar({ level: 'success', text: 'Credential revoked successfully.' })
    } catch (error) {
      showSnackbar({ level: 'error', text: errorMessage(error, 'Failed to revoke credential') })
    }
  },

  /**
   * Local-only cleanup. Does not call the API.
   *
   * Collapses the row as well: with the session gone the panel has nothing to
   * show, and the row goes back to being a plain "Connect" row.
   */
  clearSession: (connectionName) => {
    const { expanded, setExpanded } = useNativeConnectionsStore.getState()
    if (expanded === connectionName) setExpanded(null)
    set((s) => ({
      activeByName: removeFrom(s.activeByName, connectionName),
      credentialsByName: removeFrom(s.credentialsByName, connectionName),
      statusByName: removeFrom(s.statusByName, connectionName),
      reviewByName: removeFrom(s.reviewByName, connectionName),
      errorByName: removeFrom(s.errorByName, connectionName),
    }))
    get().syncExpiryWatcher()
    get().syncReviewWatcher()
  },

  resetFlow: (connectionName) => {
    set((s) => ({
      statusByName: removeFrom(s.statusByName, connectionName),
      errorByName: removeFrom(s.errorByName, connectionName),
    }))
  },
}))
