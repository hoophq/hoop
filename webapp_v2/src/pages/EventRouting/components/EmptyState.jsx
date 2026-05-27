import { Image, Stack, Text, Title } from '@mantine/core'
import { Plus } from 'lucide-react'
import Button from '@/components/Button'

export default function EmptyState({ onCreate }) {
  return (
    <Stack align="center" gap="md" py="xxlAlt">
      <Image src="/images/illustrations/empty-state.png" alt="No subscriptions" w={280} />
      <Title order={3}>No subscriptions yet</Title>
      <Text size="sm" c="dimmed" ta="center" maw={480}>
        Subscribe a runbook to platform events to react automatically. Each match becomes an
        audited runbook session under the same approval and masking guardrails.
      </Text>
      <Button leftSection={<Plus size={16} />} onClick={onCreate}>
        Create subscription
      </Button>
    </Stack>
  )
}
