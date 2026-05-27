import Badge from '@/components/Badge'

const CONFIG = {
  active: { color: 'green', label: 'Active' },
  paused: { color: 'yellow', label: 'Paused' },
  archived: { color: 'gray', label: 'Archived' },
}

export default function StatusBadge({ status }) {
  const cfg = CONFIG[status] || { color: 'gray', label: status }
  return (
    <Badge color={cfg.color} variant="light">
      {cfg.label}
    </Badge>
  )
}
