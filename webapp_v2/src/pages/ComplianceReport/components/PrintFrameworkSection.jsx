import { Badge, Card, Group, Stack, Text, Title } from '@mantine/core'
import { STATUS_META } from '../constants'
import { FrameworkScoreCard } from './FrameworkPanel'

/**
 * Export-only control row: bare status glyph, outlined ID chip, title and
 * description. No actions, tooltips or accordion chrome — the export is a
 * static snapshot (design reference: "Compliance Report" export mock).
 */
function PrintControlRow({ control }) {
  const meta = STATUS_META[control.status] ?? STATUS_META.unable_to_verify
  const Icon = meta.icon
  return (
    <Group align="flex-start" gap="sm" wrap="nowrap">
      <Icon
        size={18}
        color={`var(--mantine-color-${meta.color}-6)`}
        style={{ flexShrink: 0, marginTop: 2 }}
        aria-label={meta.label}
      />
      <Stack gap={2}>
        <Group gap="xs" wrap="nowrap">
          <Badge variant="outline" color="blue" size="sm" radius="sm" tt="none">
            {control.id}
          </Badge>
          <Text fw={600}>{control.title}</Text>
        </Group>
        <Text size="sm" c="dimmed">
          {control.description}
        </Text>
      </Stack>
    </Group>
  )
}

/** One framework in the export: score card, then flat "ID: Title" sections. */
export default function PrintFrameworkSection({ framework }) {
  return (
    <Stack gap="lgAlt">
      <FrameworkScoreCard framework={framework} />
      {(framework.groups ?? []).map((group) => (
        <Stack key={group.id} gap="sm">
          <Title order={4}>
            {group.id}: {group.title}
          </Title>
          <Card withBorder p="lgAlt">
            <Stack gap="md">
              {group.controls.map((control) => (
                <PrintControlRow key={`${group.id}-${control.id}`} control={control} />
              ))}
            </Stack>
          </Card>
        </Stack>
      ))}
    </Stack>
  )
}
