import { Card, Stack, Group, Title, Text, Center } from '@mantine/core'
import { EMPTY_CHART_MESSAGE } from '../constants'

/**
 * Shared chrome for every card on the dashboard: heading, optional subtitle
 * (the active date range) and optional right-hand control (the range filter),
 * over a fixed-height body.
 *
 * `minH` is a prop rather than a constant because the legacy cards are not
 * uniform: the Reviews and Resource Roles bodies are 300px and Redacted Data
 * is 400px.
 *
 * Renders `error` over `empty` over `children`, so a failed request is never
 * reported as "no data".
 */
export default function ChartCard({
  title,
  subtitle,
  control,
  minH,
  empty = false,
  emptyMessage = EMPTY_CHART_MESSAGE,
  error,
  children,
}) {
  const placeholder = error ?? (empty ? emptyMessage : null)

  return (
    <Card withBorder p="lgAlt" h="100%">
      <Stack gap="lgAlt" h="100%">
        <Group justify="space-between" align="flex-start" wrap="nowrap">
          <Stack gap={0}>
            <Title order={4}>{title}</Title>
            {subtitle && (
              <Text size="xs" c="dimmed">
                {subtitle}
              </Text>
            )}
          </Stack>
          {control}
        </Group>

        {placeholder ? (
          <Center mih={minH} flex={1}>
            <Text size="xs" c={error ? 'red' : 'dimmed'}>
              {placeholder}
            </Text>
          </Center>
        ) : (
          children
        )}
      </Stack>
    </Card>
  )
}
