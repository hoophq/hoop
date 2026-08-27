import { Badge as MantineBadge } from '@mantine/core'

/**
 * Semantic status badge. Use `variant` to express meaning:
 *   - "active" → green filled
 *   - "inactive" → gray outline
 *   - "warning" → yellow filled
 *   - "danger" → red filled
 * Falls back to standard Mantine props when `variant` is a Mantine variant name.
 */
const SEMANTIC_MAP = {
  active: { color: 'green', variant: 'filled' },
  inactive: { color: 'gray', variant: 'outline' },
  warning: { color: 'yellow', variant: 'filled' },
  danger: { color: 'red', variant: 'filled' },
}

export default function Badge({ variant = 'filled', color, children, ...props }) {
  const semantic = SEMANTIC_MAP[variant]
  const resolvedColor = semantic?.color ?? color
  const resolvedVariant = semantic?.variant ?? variant

  return (
    <MantineBadge variant={resolvedVariant} color={resolvedColor} size="sm" radius="sm" {...props}>
      {children}
    </MantineBadge>
  )
}
