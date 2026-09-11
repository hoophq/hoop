import { Group, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import { FEATURES } from '../config'

// The feature chips of the Figma "Sidecar Details": AI Analyzer, Data Masking,
// Guardrails, each with its icon and color. `features` is a list of keys from
// ../config.js; nothing on means "No features configured".
export default function FeaturePills({ features, emptyLabel = 'No features configured' }) {
  if (!features || features.length === 0) {
    return (
      <Text size="sm" c="dimmed">
        {emptyLabel}
      </Text>
    )
  }
  return (
    <Group gap="xs">
      {features.map((key) => {
        const feature = FEATURES[key]
        if (!feature) return null
        const Icon = feature.icon
        return (
          <Badge
            key={key}
            variant="light"
            color={feature.color}
            tt="none"
            fw={500}
            leftSection={<Icon size={12} aria-hidden="true" />}
          >
            {feature.label}
          </Badge>
        )
      })}
    </Group>
  )
}
