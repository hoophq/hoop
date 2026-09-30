import { Group, Text } from '@mantine/core'
import Avatar from '@/components/Avatar'
import Badge from '@/components/Badge'
import Tooltip from '@/components/Tooltip'
import { FEATURES } from '../config'

const AVATAR_SIZE = 24
const ICON_SIZE = 14

// `compact` is the table form: overlapping icons, the label in a tooltip.
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

  if (compact) {
    return (
      <Avatar.Group>
        {features.map((key) => {
          const feature = FEATURES[key]
          if (!feature) return null
          const Icon = feature.icon
          return (
            <Tooltip key={key} label={feature.label}>
              <Avatar size={AVATAR_SIZE} color={feature.color} variant="light" aria-label={feature.label}>
                <Icon size={ICON_SIZE} aria-hidden="true" />
              </Avatar>
            </Tooltip>
          )
        })}
      </Avatar.Group>
    )
  }

  return (
    <Group gap="xs" wrap="wrap">
      {features.map((key) => {
        const feature = FEATURES[key]
        if (!feature) return null
        const Icon = feature.icon
        return (
          <Badge key={key} tag chip variant="light" color={feature.color} icon={<Icon size={12} aria-hidden="true" />}>
            {feature.label}
          </Badge>
        )
      })}
    </Group>
  )
}
