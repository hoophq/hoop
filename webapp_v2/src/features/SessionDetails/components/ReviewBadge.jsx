import { Check, Clock2, X } from 'lucide-react'
import Badge from '@/components/Badge'
import { reviewBadgeStatus } from '../sessionState'

// `yellow` is not a palette of this theme; amber reads at 12px with the
// theme's light-variant text (theme.js).
const BADGES = {
  APPROVED: { color: 'green', icon: Check, label: 'Approved' },
  PENDING: { color: 'amber', icon: Clock2, label: 'Pending' },
  REJECTED: { color: 'red', icon: X, label: 'Rejected' },
}

// The status of a review or of one of its groups.
export default function ReviewBadge({ status }) {
  const badge = BADGES[reviewBadgeStatus(status)]
  if (!badge) return null
  const Icon = badge.icon
  return (
    <Badge color={badge.color} variant="light" tag chip icon={<Icon size={12} aria-hidden />}>
      {badge.label}
    </Badge>
  )
}
