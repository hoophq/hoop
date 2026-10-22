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
 *   showSnackbar({ level: 'approval', text: 'Approval request', actions: [{ label: 'Open', onClick: open }], duration: Infinity })
 *   showSnackbar({ id, text: 'Updated in place' })  // pass a returned id to replace that toast
 *   An action closes its toast; `keepOpen: true` on the action leaves it open.
 */
export function showSnackbar({ level = 'info', text, description, details, actions, duration, id, onDismiss } = {}) {
  const ms = duration ?? (level === 'error' ? 10000 : undefined)
  const options = {
    ...(ms ? { duration: ms } : {}),
    ...(id != null ? { id } : {}),
    ...(onDismiss ? { onDismiss } : {}),
  }
  return toast.custom(
    (toastId) => (
      <Toast
        id={toastId}
        type={level}
        title={text}
        description={description}
        details={details}
        actions={actions}
      />
    ),
    Object.keys(options).length ? options : undefined,
  )
}

export const dismissSnackbar = (id) => toast.dismiss(id)
