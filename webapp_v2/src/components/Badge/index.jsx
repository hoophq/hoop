import { Badge as MantineBadge } from '@mantine/core'
import classes from './Badge.module.css'

/**
 * Semantic status badge. Use `variant` to express meaning:
 *   - "active" → green filled
 *   - "inactive" → gray outline
 *   - "warning" → yellow filled
 *   - "danger" → red filled
 * Falls back to standard Mantine props when `variant` is a Mantine variant name.
 * `fullLabel` keeps a short label from being ellipsized (see Badge.module.css).
 */
const SEMANTIC_MAP = {
  active: { color: 'green', variant: 'filled' },
  inactive: { color: 'gray', variant: 'outline' },
  warning: { color: 'yellow', variant: 'filled' },
  danger: { color: 'red', variant: 'filled' },
}

export default function Badge({
  variant = 'filled',
  color,
  fullLabel = false,
  classNames = {},
  children,
  ...props
}) {
  const semantic = SEMANTIC_MAP[variant]
  const resolvedColor = semantic?.color ?? color
  const resolvedVariant = semantic?.variant ?? variant
  const merged = fullLabel ? { label: classes.fullLabel, ...classNames } : classNames

  return (
    <MantineBadge
      variant={resolvedVariant}
      color={resolvedColor}
      size="sm"
      radius="sm"
      classNames={merged}
      {...props}
    >
      {children}
    </MantineBadge>
  )
}
