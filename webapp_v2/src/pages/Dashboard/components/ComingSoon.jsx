import { Stack, Title, Text } from '@mantine/core'
import { ChartColumnBig } from 'lucide-react'

/** Placeholder body for the dashboard panels that have no data source yet. */
export default function ComingSoon() {
  return (
    <Stack align="center" justify="center" gap={0} flex={1}>
      <ChartColumnBig size={32} />
      <Title order={4}>Coming soon</Title>
      <Text size="xs" c="dimmed">
        Stay tuned for more insights
      </Text>
    </Stack>
  )
}
