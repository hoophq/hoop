import Badge from '@/components/Badge'
import { sessionStatusKey } from '../sessionState'

const STATUSES = {
  done: { color: 'green', label: 'Success' },
  blocked: { color: 'orange', label: 'Blocked' },
  ready: { color: 'blue', label: 'Ready' },
  error: { color: 'red', label: 'Error' },
  rejected: { color: 'red', label: 'Rejected' },
  failed: { color: 'red', label: 'Failed' },
}

export default function SessionStatusBadge({ session }) {
  const key = sessionStatusKey(session)
  const status = STATUSES[key] || { color: 'gray', label: key || 'Unknown' }
  return (
    <Badge color={status.color} variant="light" tag chip>
      {status.label}
    </Badge>
  )
}
