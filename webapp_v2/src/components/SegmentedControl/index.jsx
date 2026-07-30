import { SegmentedControl as MantineSegmentedControl, Box } from '@mantine/core'
import Tooltip from '@/components/Tooltip'
import classes from './SegmentedControl.module.css'

/**
 * Segmented control with support for *locked* items — options that are visible
 * and hoverable but not selectable, used to advertise Enterprise-gated choices.
 *
 * `data` is `[{ value, label, locked }]`. A locked item renders dimmed, shows
 * `lockedTooltip` on hover, and is dropped in `onChange` so the selection never
 * moves.
 *
 * Deliberately NOT built on Mantine's `disabled`: Mantine sets
 * `pointer-events: none` on a disabled control, which kills hover and means the
 * Tooltip would never open — the user would see a greyed-out option with no
 * explanation. Locking is therefore visual + behavioural, not `disabled`.
 * `aria-disabled` still conveys the state to assistive tech.
 *
 * Usage:
 *   <SegmentedControl
 *     value={range}
 *     onChange={setRange}
 *     lockedTooltip="Available on Enterprise plan only."
 *     data={[
 *       { value: '1', label: '24h', locked: true },
 *       { value: '7', label: '7d' },
 *     ]}
 *   />
 */
export default function SegmentedControl({ data = [], onChange, lockedTooltip, ...props }) {
  const lockedValues = new Set(data.filter((item) => item.locked).map((item) => item.value))

  const resolvedData = data.map(({ value, label, locked }) => {
    if (!locked) return { value, label }
    return {
      value,
      label: (
        <Tooltip label={lockedTooltip}>
          <Box component="span" className={classes.locked} aria-disabled="true">
            {label}
          </Box>
        </Tooltip>
      ),
    }
  })

  const handleChange = (value) => {
    if (lockedValues.has(value)) return
    onChange?.(value)
  }

  return <MantineSegmentedControl data={resolvedData} onChange={handleChange} {...props} />
}
