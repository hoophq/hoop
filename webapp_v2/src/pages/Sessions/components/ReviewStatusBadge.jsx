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
 * v1 renders `[:> Badge {:color "…" :size "2"}]` — Radix's default Badge variant
 * is `soft`, so these are tinted, not solid. Mantine's `light` is the equivalent;
 * do NOT use the semantic `active`/`warning`/`danger` variants here, they are
 * filled and read far heavier than the original (solid yellow is also barely
 * legible).
 *
 * v1 only handled APPROVED / PENDING / REJECTED and its `:default` returned nil,
 * so REVOKED, PROCESSING, EXECUTED and UNKNOWN — all valid ReviewStatusType
 * values — rendered an empty cell, making a revoked session indistinguishable
 * from an unreviewed one. Every status is mapped here, and the fallback shows
 * the raw string so a new gateway status stays visible.
 */
const STATUS_MAP = {
  PENDING: { color: 'yellow', icon: Clock2, label: 'Pending' },
  APPROVED: { color: 'green', icon: CircleCheckBig, label: 'Approved' },
  REJECTED: { color: 'red', icon: OctagonX, label: 'Rejected' },
  REVOKED: { color: 'gray', icon: Ban, label: 'Revoked' },
  PROCESSING: { color: 'gray', icon: Hourglass, label: 'Processing' },
  EXECUTED: { color: 'gray', icon: CircleCheck, label: 'Executed' },
  UNKNOWN: { color: 'gray', icon: CircleHelp, label: 'Unknown' },
}

export default function ReviewStatusBadge({ status }) {
  if (!status) return null
  const config = STATUS_MAP[status] ?? {
    color: 'gray',
    icon: CircleHelp,
    label: status,
  }
  const Icon = config.icon

  return (
    <Badge color={config.color} variant="light">
      <Group gap={4} wrap="nowrap">
        <Icon size={14} />
        {config.label}
      </Group>
    </Badge>
  )
}
