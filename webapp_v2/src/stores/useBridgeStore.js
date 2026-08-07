import { create } from 'zustand'
import { clojureDispatch } from '@/utils/clojureDispatch'

// Cross-cutting bridge calls into the legacy CLJS app. Per
// CLAUDE.md "Never call `window.hoopDispatch` directly from a
// component" — wrap every dispatch in a store method so the
// underlying mechanism can be swapped when the CLJS side is removed.
//
// Snackbars are NOT bridged: the CLJS Toaster only exists while the
// CLJS tree is mounted, so a dispatch from a React-only route would be
// lost. Use `showSnackbar` from '@/utils/snackbar' instead.
export const useBridgeStore = create(() => ({
  // Refetch the current user in the CLJS app-db. Needed after React mutates
  // user-affecting state (e.g. applying a protection profile) so CLJS events
  // like :onboarding/check-user don't act on a stale cached user. No-op when
  // the CLJS bundle isn't loaded — it fetches fresh data on first mount.
  refreshLegacyUser: () => {
    clojureDispatch('users->get-user')
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
