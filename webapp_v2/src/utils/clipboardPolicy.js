import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'

/**
 * Single owner of the org control `disable_clipboard_copy_cut` (GET /serverinfo).
 *
 * Ported from `webapp/src/webapp/events/clipboard.cljs`, which is deleted in the
 * same change: the CLJS bundle is injected lazily by ClojureApp, so a session
 * that never leaves a React route never installed the listeners and the setting
 * was silently not enforced. React is now the only owner — keeping both would
 * mean two listener sets with two independent cooldowns, i.e. two toasts for one
 * Cmd+C.
 */

// Verbatim from the CLJS original — the two implementations must not drift
// while the webclient still shows this same string from its CodeMirror keymap.
const BLOCKED_MESSAGE = 'Clipboard copy/cut operations are disabled by administrator'
const NOTIFICATION_COOLDOWN_MS = 2000

const BLOCKED_CLIPBOARD_EVENTS = ['copy', 'cut', 'beforecopy', 'beforecut']

// Module scope — the analogue of the CLJS `(defonce last-notification (atom 0))`.
// It rate-limits a document-level side effect, so it has to survive remounts and
// StrictMode's double-invoked effect: a useRef spans neither, and a store field
// would re-render the tree on every blocked keystroke for a value nothing renders.
let lastNotifiedAt = 0

export function isClipboardDisabled() {
  return useUserStore.getState().disableClipboard
}

/**
 * The "clipboard is disabled" toast.
 *
 * `rateLimited` (default true) exists for the document-event path: one Cmd+C can
 * fire both `beforecopy` and `copy`, and without the 2s window the user gets two
 * error toasts, each pinned for 10s. An explicit user action must never be rate
 * limited — a deliberate click that produces no clipboard write, no error and no
 * toast is worse than a duplicate toast.
 */
export function notifyClipboardBlocked({ rateLimited = true } = {}) {
  const now = Date.now()
  // Inverse of the CLJS `(> (- now @last-notification) notification-cooldown)`
  // guard, so the boundary behaves identically.
  if (rateLimited && now - lastNotifiedAt <= NOTIFICATION_COOLDOWN_MS) return
  lastNotifiedAt = now
  showSnackbar({ level: 'error', text: BLOCKED_MESSAGE })
}

/**
 * Stable module-level identity so add/removeEventListener always pair up.
 *
 * `stopImmediatePropagation` is deliberate, and one line stronger than the CLJS
 * original: per the Clipboard API spec a *canceled* copy/cut event still writes
 * whatever a handler placed in `event.clipboardData` via `setData()`, and
 * CodeMirror 6 does exactly that on its contentDOM. Registered in the capture
 * phase this runs before any descendant listener and cuts propagation, so
 * nothing downstream ever gets to populate the clipboard — which is what closes
 * right-click > Copy inside the webclient editor.
 */
function handleBlockedClipboardEvent(event) {
  event.preventDefault()
  event.stopImmediatePropagation()
  notifyClipboardBlocked()
}

// Refcounted because the handler identity is shared: addEventListener dedups on
// (type, listener, capture), so N callers produce ONE listener set and a naive
// cleanup would silently disable the control for everyone still expecting it.
let installCount = 0

export function installClipboardGuard() {
  installCount += 1
  if (installCount === 1) {
    for (const type of BLOCKED_CLIPBOARD_EVENTS) {
      document.addEventListener(type, handleBlockedClipboardEvent, true)
    }
  }

  let released = false
  return function uninstallClipboardGuard() {
    if (released) return
    released = true
    installCount -= 1
    if (installCount === 0) {
      for (const type of BLOCKED_CLIPBOARD_EVENTS) {
        // The `true` must match the registration — listeners are keyed on
        // (type, listener, capture), so a bubble-phase removal silently leaks.
        document.removeEventListener(type, handleBlockedClipboardEvent, true)
      }
    }
  }
}

/**
 * The only sanctioned programmatic clipboard write in the app.
 *
 * `navigator.clipboard.writeText` is a separate code path that the document
 * listeners above cannot observe, so it needs its own policy check. Resolves
 * `false` when blocked — the user has already been notified.
 */
export function copyToClipboard(text) {
  if (isClipboardDisabled()) {
    notifyClipboardBlocked({ rateLimited: false })
    return Promise.resolve(false)
  }
  return navigator.clipboard.writeText(text).then(() => true)
}
