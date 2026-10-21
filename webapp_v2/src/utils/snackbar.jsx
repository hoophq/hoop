import { toast } from 'sonner'
import Toast from '@/components/Snackbar/Toast'

/**
 * Application snackbar / toast. Backed by `sonner` (same library as the
 * legacy CLJS app), but rendered through our own `Toast` component so
 * the visual matches `webapp.components.toast` in v1.
 *
 * Do NOT call Mantine's `notifications.show()` — it produces a different
 * visual style and breaks parity with v1.
 *
 * Usage:
 *   import { showSnackbar } from '@/utils/snackbar'
 *
 *   showSnackbar({ level: 'success', text: 'AI Agent deactivated.' })
 *   showSnackbar({ level: 'error',   text: 'Failed to load.', description: err.message })
 *   showSnackbar({ level: 'error',   text: 'Validation failed.', details: { field: 'name', reason: 'required' } })
 *   showSnackbar({ text: 'Approval request', actions: [{ label: 'Open', onClick: open }], duration: 20000 })
 */
export function showSnackbar({ level = 'info', text, description, details, actions, duration } = {}) {
  const ms = duration ?? (level === 'error' ? 10000 : undefined)
  return toast.custom(
    (id) => (
      <Toast
        id={id}
        type={level}
        title={text}
        description={description}
        details={details}
        actions={actions}
      />
    ),
    ms ? { duration: ms } : undefined,
  )
}
