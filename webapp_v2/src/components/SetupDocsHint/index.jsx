import { Anchor, Group, Text } from '@mantine/core'

export default function SetupDocsHint({ topic, href }) {
  return (
    <Group gap={4} wrap="nowrap">
      <Text size="sm" c="dimmed">{`Learn more about ${topic} setup in`}</Text>
      <Anchor size="sm" href={href} target="_blank" rel="noopener noreferrer">
        our Docs ↗
      </Anchor>
    </Group>
  )
}
