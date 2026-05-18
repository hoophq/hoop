import { Badge } from '@mantine/core'

const CONFIG = {
  ai: { color: 'indigo', label: 'AI' },
  alert: { color: 'red', label: 'Alert' },
  access: { color: 'yellow', label: 'Access' },
  session: { color: 'blue', label: 'Session' },
  connection: { color: 'gray', label: 'Connection' },
}

export default function CategoryBadge({ category }) {
  const cfg = CONFIG[category] || { color: 'gray', label: category }
  return (
    <Badge color={cfg.color} variant="light">
      {cfg.label}
    </Badge>
  )
}
