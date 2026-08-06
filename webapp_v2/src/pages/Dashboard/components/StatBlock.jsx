import { Stack, Title, Text } from '@mantine/core'

/**
 * One figure in the "Today's overview" card: heading, caption, big number.
 *
 * A null `value` renders an em dash rather than 0 — the request either has not
 * resolved or failed, and "unknown" must not read as "none".
 */
export default function StatBlock({ title, caption, value }) {
  return (
    <Stack gap="xsAlt" miw={300}>
      <Stack gap={0}>
        <Title order={4}>{title}</Title>
        <Text size="xs" c="dimmed">
          {caption}
        </Text>
      </Stack>
      <Text fz="h1" fw={700}>
        {value ?? '—'}
      </Text>
    </Stack>
  )
}
