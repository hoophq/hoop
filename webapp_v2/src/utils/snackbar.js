import { toast } from 'sonner'

/**
 * Application snackbar / toast. Backed by `sonner` — the same library
 * the legacy CLJS app uses, so React and CLJS render visually consistent
 * notifications.
 *
 * Do NOT call Mantine's `notifications.show()` — it produces a different
 * visual style and breaks parity with v1.
 *
 * Usage:
 *   import { showSnackbar } from '@/utils/snackbar'
 *
 *   showSnackbar({ level: 'success', text: 'AI Agent deactivated.' })
 *   showSnackbar({ level: 'error', text: 'Failed to load.', description: 'Network error.' })
 */
export function showSnackbar({ level, text, description } = {}) {
  const options = description ? { description } : undefined
  switch (level) {
    case 'success':
      return toast.success(text, options)
    case 'error':
      return toast.error(text, options)
    case 'info':
      return toast.info(text, options)
    default:
      return toast(text, options)
  }
}
