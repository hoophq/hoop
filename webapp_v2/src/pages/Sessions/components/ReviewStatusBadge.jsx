import { Group } from '@mantine/core'
import {
  Ban,
  CircleCheck,
  CircleCheckBig,
  CircleHelp,
  Clock2,
  Hourglass,
  OctagonX,
} from 'lucide-react'
import Badge from '@/components/Badge'

// Second consumer: Wave 6 session details header (B6.1).

/**
 * Port of the `access-request-badge` defmulti (session_item.cljs:12-32).
 *
 * v1 only handled APPROVED / PENDING / REJECTED and its `:default` returned nil,
 * so REVOKED, PROCESSING, EXECUTED and UNKNOWN — all valid ReviewStatusType
 * values — rendered an empty cell. A reviewed-then-revoked session was
 * indistinguishable from one with no review at all. Every status is mapped here,
 * and the fallback renders the raw string so a new gateway status is visible
 * rather than silently swallowed.
 */
const STATUS_MAP = {
  PENDING: { variant: 'warning', icon: Clock2, label: 'Pending' },
  APPROVED: { variant: 'active', icon: CircleCheckBig, label: 'Approved' },
  REJECTED: { variant: 'danger', icon: OctagonX, label: 'Rejected' },
  REVOKED: { variant: 'inactive', icon: Ban, label: 'Revoked' },
  PROCESSING: { variant: 'inactive', icon: Hourglass, label: 'Processing' },
  EXECUTED: { variant: 'inactive', icon: CircleCheck, label: 'Executed' },
  UNKNOWN: { variant: 'inactive', icon: CircleHelp, label: 'Unknown' },
}

export default function ReviewStatusBadge({ status }) {
  if (!status) return null
  const config = STATUS_MAP[status] ?? {
    variant: 'inactive',
    icon: CircleHelp,
    label: status,
  }
  const Icon = config.icon

  return (
    <Badge variant={config.variant}>
      <Group gap={4} wrap="nowrap">
        <Icon size={12} />
        {config.label}
      </Group>
    </Badge>
  )
}
