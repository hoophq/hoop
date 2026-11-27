import { useEffect } from 'react'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'

const CLIPBOARD_EVENTS = ['copy', 'cut', 'beforecopy', 'beforecut']
const NOTICE_COOLDOWN_MS = 2000

export function createClipboardBlocker({ notify, now = Date.now }) {
  let lastNotice = -Infinity
  return (event) => {
    event.preventDefault()
    const t = now()
    if (t - lastNotice <= NOTICE_COOLDOWN_MS) return
    lastNotice = t
    notify()
  }
}

// With disable_clipboard_copy_cut on, blocks copy and cut on the whole page, as
// CLJS events/clipboard.cljs does.
export function useClipboardBlock() {
  const disabled = useUserStore((s) => s.disableClipboard)

  useEffect(() => {
    if (!disabled) return undefined
    const block = createClipboardBlocker({
      notify: () => showSnackbar({ level: 'error', text: 'Copy and cut are disabled by your administrator.' }),
    })
    CLIPBOARD_EVENTS.forEach((type) => document.addEventListener(type, block))
    return () => CLIPBOARD_EVENTS.forEach((type) => document.removeEventListener(type, block))
  }, [disabled])
}
