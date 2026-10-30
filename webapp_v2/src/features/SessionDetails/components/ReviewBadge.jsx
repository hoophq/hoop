import { Check, Clock2, X } from 'lucide-react'
import Badge from '@/components/Badge'

// `yellow` is not a palette of this theme; amber with darker text reads at 12px
// (components/Badge).
const BADGES = {
  APPROVED: { color: 'green', c: 'green.9', icon: Check, label: 'Approved' },
  PENDING: { color: 'amber', c: 'amber.9', icon: Clock2, label: 'Pending' },
  REJECTED: { color: 'red', c: 'red.9', icon: X, label: 'Rejected' },
}

// The status of a review or of one of its groups. Other statuses (REVOKED,
// PROCESSING, EXECUTED, UNKNOWN, EXPIRED) draw nothing.
export default function ReviewBadge({ status }) {
  const badge = BADGES[status]
  if (!badge) return null
  const Icon = badge.icon
  return (
    <Badge color={badge.color} c={badge.c} variant="light" tag chip icon={<Icon size={12} aria-hidden />}>
      {badge.label}
    </Badge>
  )
}
