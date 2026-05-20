import { Box, Card, Group, Stack, Text, Title } from '@mantine/core'
import Code from '@/components/Code'
import Modal from '@/components/Modal'
import { useEventRoutingStore } from '../store'
import CategoryBadge from './CategoryBadge'

export default function EventDetailModal() {
  const evt = useEventRoutingStore((s) => s.eventDetailTarget)
  const setEventDetail = useEventRoutingStore((s) => s._setEventDetailTarget)
  const onClose = () => setEventDetail(null)

  if (!evt) return <Modal opened={false} onClose={onClose} title="" size="lg" />

  const sampleJson = (() => {
    try {
      return JSON.stringify(evt.samplePayload, null, 2)
    } catch {
      return '{}'
    }
  })()

  return (
    <Modal opened={!!evt} onClose={onClose} title={<Code bg="indigo.1" c="indigo.9">{evt.name}</Code>} size="lg">
      <Stack gap="md">
        <Group justify="space-between" align="flex-start">
          <Text size="sm" c="dimmed" style={{ flex: 1 }}>{evt.description}</Text>
          <CategoryBadge category={evt.category} />
        </Group>

        <Stack gap={4}>
          <Title order={5}>Schema</Title>
          <Card padding="sm" withBorder>
            <Stack gap={0}>
              {(evt.schema || []).map((field) => (
                <Group
                  key={field.name}
                  px="sm"
                  py="xs"
                  style={{ borderBottom: '1px solid var(--mantine-color-default-border)' }}
                  wrap="nowrap"
                >
                  <Group gap="xs" w="40%">
                    <Code>{field.name}</Code>
                    {field.required && <Text size="xs" c="red">*</Text>}
                  </Group>
                  <Text size="xs" c="dimmed">{field.type}</Text>
                </Group>
              ))}
            </Stack>
          </Card>
        </Stack>

        <Stack gap={4}>
          <Title order={5}>Sample payload</Title>
          <Box
            p="sm"
            bg="dark.7"
            c="gray.0"
            style={{
              borderRadius: 'var(--mantine-radius-sm)',
              fontFamily: 'var(--mantine-font-family-monospace)',
              fontSize: 12,
              lineHeight: 1.55,
              overflow: 'auto',
            }}
          >
            <pre style={{ margin: 0 }}>{sampleJson}</pre>
          </Box>
        </Stack>
      </Stack>
    </Modal>
  )
}
