// Range options for the Reviews and Redacted Data charts. Values are the number
// of days, as strings, because SegmentedControl works on string values.
export const RANGE_OPTIONS = [
  { value: '1', label: '24h' },
  { value: '7', label: '7d' },
  { value: '14', label: '14d' },
  { value: '30', label: '30d' },
  { value: '90', label: '3m' },
]

export const DEFAULT_RANGE = '7'

// The only range a free-license organization may select; every other option is
// rendered locked with FREE_TIER_TOOLTIP.
export const FREE_LICENSE_RANGE = DEFAULT_RANGE

export const FREE_TIER_TOOLTIP = 'Available on Enterprise plan only.'

export const FREE_LICENSE_MESSAGE =
  'Organizations with Free plan have limited reporting. Upgrade to Enterprise to have unlimited access to the Dashboard.'

export const EMPTY_CHART_MESSAGE = 'No data found for the selected period'

// Bucket for connections whose `subtype` is empty.
export const OTHER_SUBTYPE = 'others'
