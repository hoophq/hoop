import { Stack, Text, Title, Code, List } from '@mantine/core'
import { Construction } from 'lucide-react'

/**
 * A route that exists in the information architecture but has no backend yet.
 *
 * It says which project owes the work and what is missing, and renders nothing
 * that could be mistaken for real state: no empty table, no zero counter, no
 * spinner that never resolves. A page that pretends to have loaded is worse
 * than one that says it cannot.
 */
export default function NotImplemented({ title, project, missing = [] }) {
  return (
    <Stack align="center" justify="center" mih="60vh" gap="md" p="xl">
      <Construction size={32} aria-hidden="true" />
      <Stack align="center" gap={4} maw={520}>
        <Title order={3}>{title}</Title>
        <Text size="sm" c="dimmed" ta="center">
          {`This page has a route and a place in the navigation, but no data behind it yet. It is delivered by the ${project} project.`}
        </Text>
      </Stack>
      {missing.length > 0 && (
        <Stack gap="xs" maw={520} w="100%">
          <Text size="sm" fw={600}>Waiting on</Text>
          <List size="sm" c="dimmed">
            {missing.map((item) => (
              <List.Item key={item}><Code>{item}</Code></List.Item>
            ))}
          </List>
        </Stack>
      )}
    </Stack>
  )
}
