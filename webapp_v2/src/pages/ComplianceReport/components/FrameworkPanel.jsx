import { Accordion, Card, Divider, Group, Progress, Stack, Text, Title } from '@mantine/core'
import { LEVEL_META } from '../constants'
import { ControlRow } from './ControlBits'

/**
 * One framework view: score bar with status breakdown, then control-family
 * sections. All sections start expanded — the report is meant to be scanned
 * (and printed) whole, not hunted through. `showDetails` inlines each
 * control's message and evidence; the print export uses it because tooltips
 * do not print.
 */
export default function FrameworkPanel({ framework, showDetails = false }) {
  const level = LEVEL_META[framework.level] ?? LEVEL_META.low
  const groups = framework.groups ?? []

  return (
    <Stack gap="lgAlt">
      <Card withBorder p="lgAlt">
        <Stack gap="sm">
          <Group justify="space-between" align="flex-start" wrap="nowrap">
            <Stack gap={2}>
              <Text size="lg" fw={700}>
                {framework.name} Compliance Score
              </Text>
              <Text size="sm" c="dimmed">
                Based on current control status and active threats
              </Text>
            </Stack>
            <Title order={2}>{framework.score_percent}%</Title>
          </Group>
          <Progress value={framework.score_percent} color={level.color} size="lg" radius="xl" />
          <Group justify="space-between">
            <Text size="xs" c="dimmed" tt="uppercase">
              Low
            </Text>
            <Text size="xs" c="dimmed" tt="uppercase">
              Moderate
            </Text>
            <Text size="xs" c="dimmed" tt="uppercase">
              Strong
            </Text>
          </Group>
        </Stack>
      </Card>

      <Accordion
        multiple
        defaultValue={groups.map((g) => g.id)}
        variant="separated"
        radius="md"
      >
        {groups.map((group) => (
          <Accordion.Item key={group.id} value={group.id}>
            <Accordion.Control>
              <Group gap="xs">
                <Text fw={600}>{group.title}</Text>
                <Text size="sm" c="dimmed">
                  {group.controls.length} controls
                </Text>
              </Group>
            </Accordion.Control>
            <Accordion.Panel>
              <Stack gap="md">
                {group.controls.map((control, idx) => (
                  <Stack key={`${group.id}-${control.id}`} gap="md">
                    {idx > 0 && <Divider />}
                    <ControlRow control={control} showDetails={showDetails} />
                  </Stack>
                ))}
              </Stack>
            </Accordion.Panel>
          </Accordion.Item>
        ))}
      </Accordion>
    </Stack>
  )
}
