import { Group, Stack, Text, Title } from '@mantine/core'
import { Play } from 'lucide-react'
import Button from '@/components/Button'

const MB = 1024 * 1024

// The recording before it plays: frames, size and resolution (matrix P01).
export default function RdpSummary({ bitmaps, bytes, width, height, onPlay }) {
  const facts = [`${bitmaps} bitmap frames recorded`, `${(bytes / MB).toFixed(2)} MB data`, `${width}x${height} resolution`]
  return (
    <Stack align="center" gap="md" py="xl">
      <Stack align="center" gap="xs">
        <Title order={3} fz="xl">
          RDP Session Recording
        </Title>
        <Group gap="xs" justify="center">
          {facts.map((fact, i) => (
            <Group key={fact} gap="xs" wrap="nowrap">
              {i > 0 && (
                <Text c="dimmed" aria-hidden="true">
                  •
                </Text>
              )}
              <Text c="dimmed">{fact}</Text>
            </Group>
          ))}
        </Group>
      </Stack>
      <Text size="sm" c="dimmed">
        RDP session recordings are stored as bitmap frames for replay.
      </Text>
      <Button leftSection={<Play size={16} />} onClick={onPlay}>
        Play Recording
      </Button>
    </Stack>
  )
}
