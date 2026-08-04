import { Box, Group, ScrollArea, Text } from '@mantine/core'

/**
 * Port of the runbook parameters strip (session_details.cljs:295-307).
 *
 * v1 parses `session.labels.runbookParameters` as JSON and renders nothing when
 * it is absent or empty. A malformed value throws there; here it is treated as
 * absent — the strip is informational and not worth taking the modal down for.
 */
export default function RunbookParameters({ session }) {
  const raw = session?.labels?.runbookParameters
  if (!raw) return null

  let params
  try {
    params = JSON.parse(raw)
  } catch {
    return null
  }
  const entries = Object.entries(params ?? {})
  if (!entries.length) return null

  return (
    <Group gap="md" align="center" wrap="nowrap">
      <Text w={128} size="sm" fw={700}>
        Parameters
      </Text>
      <ScrollArea>
        <Group gap="md" wrap="nowrap">
          {entries.map(([key, value]) => (
            <Box key={key}>
              <Text span size="xs" fw={700} c="dimmed">
                {`${key}: `}
              </Text>
              <Text span size="xs">
                {String(value)}
              </Text>
            </Box>
          ))}
        </Group>
      </ScrollArea>
    </Group>
  )
}
