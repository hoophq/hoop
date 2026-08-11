import { useEffect } from 'react'
import { useNativeConnectionsStore } from '@/stores/useNativeConnectionsStore'
import { useNativeAccessStore } from '@/stores/useNativeAccessStore'

/**
 * CLJS → React bridge for native access.
 *
 * The pages that still own their routes in ClojureScript (/resources,
 * /sessions/:id, the CLJS command palette) have to be able to open this drawer.
 * The existing bridge only runs the other way (window.hoopDispatch), so this
 * reuses the documented CLJS → React contract instead: a DOM CustomEvent on
 * window, the same mechanism as `hoop:session-executed`.
 *
 * The CLJS side emits these only when window.__hoopReactShellPresent is set, so
 * standalone shadow-cljs keeps its own modal. Firing with no listener attached
 * is a harmless no-op, which is the right way for this to degrade.
 *
 * This outlives the CLJS native-access code: /resources and /sessions/:id stay
 * ClojureScript well past this ticket.
 */
export const NATIVE_ACCESS_OPEN_EVENT = 'hoop:native-access-open'
export const NATIVE_ACCESS_RESUME_EVENT = 'hoop:native-access-resume'

export function useCljsBridge() {
  useEffect(() => {
    // "Connect natively to X" from a CLJS page: open the drawer and run the
    // same flow the row's own button runs, dialog included.
    const onOpen = (event) => {
      const { connectionName } = event.detail || {}
      if (!connectionName) return
      useNativeAccessStore.getState().openAndConnect(connectionName)
    }

    // Post-approval resume. accessDurationSec travels in the detail because it
    // comes from the CLJS session-details review payload, which React cannot
    // read.
    const onResume = (event) => {
      const { connectionName, sessionId, accessDurationSec } = event.detail || {}
      if (!connectionName || !sessionId) return
      useNativeConnectionsStore.getState().openConnection(connectionName)
      useNativeAccessStore
        .getState()
        .resumeAfterReview(connectionName, sessionId, accessDurationSec)
    }

    window.addEventListener(NATIVE_ACCESS_OPEN_EVENT, onOpen)
    window.addEventListener(NATIVE_ACCESS_RESUME_EVENT, onResume)
    return () => {
      window.removeEventListener(NATIVE_ACCESS_OPEN_EVENT, onOpen)
      window.removeEventListener(NATIVE_ACCESS_RESUME_EVENT, onResume)
    }
  }, [])
}
