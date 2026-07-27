import { create } from 'zustand'
import { clojureDispatch } from '@/utils/clojureDispatch'

// Cross-cutting bridge calls into the legacy CLJS app. Per
// CLAUDE.md "Never call `window.hoopDispatch` directly from a
// component" — wrap every dispatch in a store method so the
// underlying mechanism can be swapped when the CLJS side is removed.
//
// snackbar: dispatched to CLJS so React pages share the same look
// (top-right, 10s, dark) as the legacy snackbar component. Levels
// match the CLJS handler at events/components/toast.cljs: 'success',
// 'error', 'info'.
export const useBridgeStore = create(() => ({
  showSnackbar: ({ level, text, details, description }) => {
    clojureDispatch('show-snackbar', { level, text, details, description })
  },

  // Refetch the current user in the CLJS app-db. Needed after React mutates
  // user-affecting state (e.g. applying a protection profile) so CLJS events
  // like :onboarding/check-user don't act on a stale cached user. No-op when
  // the CLJS bundle isn't loaded — it fetches fresh data on first mount.
  refreshLegacyUser: () => {
    clojureDispatch('users->get-user')
  },

  // Open the CLJS native-client-access modal for a connection (the same flow
  // the Resources page "Connect" button triggers). Handler:
  // webapp/src/webapp/connections/native_client_access/events.cljs.
  openNativeClientAccess: (connectionName) => {
    clojureDispatch('native-client-access->start-flow', connectionName)
  },

  // Same as above, but tolerates the CLJS bundle still booting. The modal
  // renders inside the CLJS host, which only mounts on CLJS-owned routes —
  // so callers that navigate to one first (e.g. /resources) need to wait for
  // window.hoopDispatch before dispatching. Aborts when the user has already
  // navigated away from `expectPath` (the modal must not pop on an unrelated
  // route) and gives up silently after ~5s: the user has landed on a page
  // whose own Connect button runs the identical flow.
  openNativeClientAccessWhenReady: (connectionName, { expectPath = '/resources' } = {}) => {
    const deadline = Date.now() + 5000
    const attempt = () => {
      if (!window.location.pathname.startsWith(expectPath)) return
      if (typeof window.hoopDispatch === 'function') {
        clojureDispatch('native-client-access->start-flow', connectionName)
        return
      }
      if (Date.now() < deadline) {
        setTimeout(attempt, 200)
      } else {
        console.warn('[useBridgeStore] CLJS never became ready, native client access modal skipped')
      }
    }
    attempt()
  },

  // Re-run the web terminal's URL-based connection selection (?role=...).
  // The CLJS editor only reads ?role= when its panel mounts — if the panel is
  // already mounted (user is on /client) or parked from a previous visit, a
  // plain navigate updates the URL without CLJS noticing. Dispatching the
  // init event re-reads window.location.search and selects the connection.
  // No-op while the bundle isn't booted: the panel's own mount init will pick
  // the role up from the URL in that case.
  syncPrimaryConnectionFromUrl: () => {
    if (typeof window.hoopDispatch === 'function') {
      clojureDispatch('primary-connection/initialize-from-query-or-persistence')
    }
  },
}))
