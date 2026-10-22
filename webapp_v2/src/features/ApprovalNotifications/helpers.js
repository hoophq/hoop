import { isSidecarReview, reviewSource } from '@/pages/Reviews/helpers'

const TYPE_LABELS = { onetime: 'One-time access', jit: 'Time-based access' }

// On a sidecar review the requester is the sidecar, as in the review modal.
export function describeReview(review) {
  const sidecars = new Map([[review.sidecar_id, { name: review.requester?.name }]])
  const source = reviewSource(review, sidecars)
  const parts = isSidecarReview(review)
    ? [source.sidecar ? `Sidecar ${source.sidecar}` : 'Sidecar', source.listener, source.resource, 'Statement']
    : [
        review.requester?.name || review.requester?.email,
        source.resource,
        TYPE_LABELS[review.type] ?? review.type,
      ]
  return { title: 'Approval request', body: parts.filter(Boolean).join(' · ') }
}

export const isVisible = () => document.visibilityState === 'visible'

export const isAttended = () => isVisible() && document.hasFocus()

const hasNotifications = () => typeof Notification !== 'undefined' && window.isSecureContext

export const notificationsGranted = () => hasNotifications() && Notification.permission === 'granted'

export const canAskPermission = () => hasNotifications() && Notification.permission === 'default'
