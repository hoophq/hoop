import { isSidecarReview, reviewSource } from '@/pages/Reviews/helpers'
import { useUserStore } from '@/stores/useUserStore'

const TYPE_LABELS = { onetime: 'One-time access', jit: 'Time-based access' }

// On a sidecar review the requester is the sidecar, as in the review modal.
export function describeReview(review) {
  const sidecars = new Map([[review.sidecar_id, { name: review.requester?.name }]])
  const source = reviewSource(review, sidecars)
  const sidecar = isSidecarReview(review)
  const who = sidecar
    ? source.sidecar ? `Sidecar ${source.sidecar}` : 'Sidecar'
    : review.requester?.name || review.requester?.email
  const where = (sidecar ? [source.listener, source.resource] : [source.resource]).filter(Boolean)
  const kind = sidecar ? 'Statement' : TYPE_LABELS[review.type] ?? review.type
  return {
    title: 'Approval request',
    body: [who, ...where, kind].filter(Boolean).join(' · '),
    who,
    where,
    kind,
  }
}

export const isVisible = () => document.visibilityState === 'visible'

export const isAttended = () => isVisible() && document.hasFocus()

const hasNotifications = () => typeof Notification !== 'undefined' && window.isSecureContext

export const notificationsGranted = () => hasNotifications() && Notification.permission === 'granted'

export const canAskPermission = () => hasNotifications() && Notification.permission === 'default'

// "Not now" on the desktop-alerts ask, kept per user in this browser. Read
// on every check, so a choice made in another tab applies here too.
const ASK_KEY = 'hoop-desktop-alerts-ask'
const askKey = () => `${ASK_KEY}:${useUserStore.getState().user?.id ?? ''}`
// Without storage, the answer lasts until the page reloads.
const declinedHere = new Set()

function declined() {
  const key = askKey()
  if (declinedHere.has(key)) return true
  try {
    return localStorage.getItem(key) === 'not-now'
  } catch {
    return false
  }
}

export const shouldAskDesktop = () => canAskPermission() && !declined()

export const isDesktopAskKey = (key) => Boolean(key?.startsWith(ASK_KEY))

export function declineDesktop() {
  const key = askKey()
  declinedHere.add(key)
  try {
    localStorage.setItem(key, 'not-now')
  } catch {
    // Kept in declinedHere.
  }
}
