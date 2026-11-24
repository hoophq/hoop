import { Anchor, Group, Text } from '@mantine/core'

export default function SetupDocsHint({ topic, href }) {
  return (
    <Group gap={4} wrap="nowrap">
      <Text size="sm" c="dimmed">{`Read how to set up ${topic} in`}</Text>
      <Anchor size="sm" href={href} target="_blank" rel="noopener noreferrer">
        the docs ↗
      </Anchor>
    </Group>
  )
}
