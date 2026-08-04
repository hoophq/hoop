import { useEffect } from 'react'
import { useUserStore } from '@/stores/useUserStore'
import { installClipboardGuard } from '@/utils/clipboardPolicy'

/**
 * Enforces the org setting `disable_clipboard_copy_cut` by blocking
 * copy/cut/beforecopy/beforecut at the document level — which covers React
 * routes and the parked CLJS bundle alike, since `document` sits above both
 * trees.
 *
 * Call once, from `App.jsx`. App is the only component mounted on every route:
 * `Layout` does not wrap `/onboarding/*` (a CLJS route) or
 * `/onboarding/protection-rules`, and `PageLayout` does not wrap the catch-all.
 * The install is refcounted in `clipboardPolicy.js` so a stray second call site
 * is safe, but it is still wrong — the flag would then be read from two places.
 * Do not add one.
 *
 * Reactive, not fire-once: `setServerInfo` runs inside `ProtectedRoute`'s
 * `initialize()` after App has mounted, so a one-shot read would always see
 * `false`. It also tears the listeners down if the flag goes back to false.
 */
export function useClipboardGuard() {
  const disableClipboard = useUserStore((s) => s.disableClipboard)

  useEffect(() => {
    if (!disableClipboard) return
    return installClipboardGuard()
  }, [disableClipboard])
}
