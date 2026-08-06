import SegmentedControl from '@/components/SegmentedControl'
import { RANGE_OPTIONS, FREE_LICENSE_RANGE, FREE_TIER_TOOLTIP } from '../constants'

/**
 * Date-range selector for a chart card.
 *
 * Free-license organizations may only use the default range; the rest stay
 * visible but locked, so the upgrade path is discoverable rather than hidden.
 * Each chart owns its own instance — the two ranges move independently, as they
 * did in the legacy app.
 */
export default function RangeFilter({ value, onChange, isFreeLicense }) {
  const data = RANGE_OPTIONS.map((option) => ({
    ...option,
    locked: isFreeLicense && option.value !== FREE_LICENSE_RANGE,
  }))

  return (
    <SegmentedControl
      size="xs"
      value={value}
      onChange={onChange}
      data={data}
      lockedTooltip={FREE_TIER_TOOLTIP}
    />
  )
}
