import { Anchor, Group, Stack, Text, Title } from '@mantine/core'
import { ArrowUpRight } from 'lucide-react'

export default function StepHeader({ title, description, docs }) {
  return (
    <Stack gap="xs">
      <Title order={2}>{title}</Title>
      <Text size="md">{description}</Text>
      {docs && (
        <Group gap={4}>
          <Text size="sm" c="dimmed">
            Access
          </Text>
          <Anchor size="sm" href={docs.href} target="_blank" rel="noopener noreferrer">
            <Group gap={2} component="span" display="inline-flex">
              our Docs
              <ArrowUpRight size={12} />
            </Group>
          </Anchor>
          <Text size="sm" c="dimmed">
            {`to learn more about ${docs.topic}.`}
          </Text>
        </Group>
      )}
    </Stack>
  )
}
