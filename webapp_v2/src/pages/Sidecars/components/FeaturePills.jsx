import { Group, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import Tooltip from '@/components/Tooltip'
import { FEATURES } from '../config'

/**
 * The feature chips of the Figma "Sidecar Details": AI Analyzer, Data Masking,
 * Guardrails, each with its icon and color. `features` is a list of keys from
 * ../config.js; nothing on means "No features configured".
 *
 * `compact` drops the labels and keeps the icons, which the Badge wrapper then
 * renders as a square chip. A table row has one narrow column for this, and
 * three labelled chips wrap onto three lines there — tripling the height of
 * every row on a sidecar that has fifty listeners. The label moves to a tooltip
 * rather than disappearing.
 */
export default function FeaturePills({ features, compact, emptyLabel = 'No features configured' }) {
  if (!features || features.length === 0) {
    // A row with no features says so by staying empty. The sentence is for the
    // details card, where a label sits beside it and would otherwise dangle.
    return compact ? null : (
      <Text size="sm" c="dimmed">
        {emptyLabel}
      </Text>
    )
  }
  return (
    <Group gap="xs" wrap={compact ? 'nowrap' : 'wrap'}>
      {features.map((key) => {
        const feature = FEATURES[key]
        if (!feature) return null
        const Icon = feature.icon
        const badge = (
          <Badge
            key={key}
            tag
            chip
            variant="light"
            color={feature.color}
            icon={<Icon size={12} aria-hidden="true" />}
            aria-label={compact ? feature.label : undefined}
          >
            {compact ? null : feature.label}
          </Badge>
        )
        // Tooltip takes over the key because it becomes the listed element; the
        // one on the badge is then the harmless key of a single child.
        return compact ? (
          <Tooltip key={key} label={feature.label}>
            {badge}
          </Tooltip>
        ) : (
          badge
        )
      })}
    </Group>
  )
}
