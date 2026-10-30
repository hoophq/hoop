import Badge from '@/components/Badge'
import { sessionStatusKey } from '../sessionState'

const STATUSES = {
  done: { color: 'green', c: 'green.9', label: 'Success' },
  blocked: { color: 'orange', c: 'orange.9', label: 'Blocked' },
  ready: { color: 'blue', c: 'blue.9', label: 'Ready' },
  error: { color: 'red', c: 'red.9', label: 'Error' },
  rejected: { color: 'red', c: 'red.9', label: 'Rejected' },
  failed: { color: 'red', c: 'red.9', label: 'Failed' },
}

export default function SessionStatusBadge({ session }) {
  const key = sessionStatusKey(session)
  const status = STATUSES[key] || { color: 'gray', label: key || 'Unknown' }
  return (
    <Badge color={status.color} c={status.c} variant="light" tag chip>
      {status.label}
    </Badge>
  )
}
