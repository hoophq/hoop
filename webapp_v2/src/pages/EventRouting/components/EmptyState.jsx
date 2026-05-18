import { Button, Stack, Text, Title } from '@mantine/core'
import { Plus, Webhook } from 'lucide-react'
import Code from '@/components/Code'

export default function EmptyState({ onCreate }) {
  return (
    <Stack align="center" gap="md" py="xxl">
      <Webhook size={36} strokeWidth={1.5} color="var(--mantine-color-gray-6)" />
      <Title order={3}>No subscriptions yet</Title>
      <Text size="sm" c="dimmed" ta="center" maw={480}>
        Subscribe a runbook to platform events to react automatically. Each match becomes an
        audited runbook session under the same approval and masking guardrails.
      </Text>
      <Button leftSection={<Plus size={16} />} onClick={onCreate}>
        Create subscription
      </Button>
      <Text size="xs" c="dimmed">
        Want a tour first? Open the <Code>Event catalog</Code> tab to see what events are available.
      </Text>
    </Stack>
  )
}
