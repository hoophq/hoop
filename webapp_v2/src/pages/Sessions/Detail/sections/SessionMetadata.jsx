import { Anchor, Divider, Group, Stack, Text } from '@mantine/core'

/**
 * Port of the metadata rows (session_details.cljs:311-332).
 *
 * These three keys are deliberately excluded from display: they drive the
 * credentials block and the "Credentials Session" detail row instead, and v1
 * hides the section entirely when nothing else remains.
 */
const HIDDEN_KEYS = new Set([
  'credentials_expire_at',
  'credentials_revoked_at',
  'credential_session',
])

/** v1 uses the `is-url-http` npm package; the check it performs is this one. */
const isHttpUrl = (value) => {
  if (typeof value !== 'string') return false
  try {
    const { protocol } = new URL(value)
    return protocol === 'http:' || protocol === 'https:'
  } catch {
    return false
  }
}

export default function SessionMetadata({ session }) {
  const entries = Object.entries(session?.metadata ?? {}).filter(
    ([key]) => !HIDDEN_KEYS.has(key)
  )
  if (!entries.length) return null

  return (
    <Stack gap={0}>
      {entries.map(([key, value], index) => (
        <Stack key={key} gap={0}>
          {index === 0 && <Divider />}
          <Group gap="xs" align="center" wrap="nowrap" py="xs">
            <Text w={128} size="sm" fw={700}>
              {key}
            </Text>
            <Divider orientation="vertical" />
            <Text size="xs" component="div">
              {isHttpUrl(value) ? (
                <Anchor href={value} target="_blank" size="xs">
                  {value}
                </Anchor>
              ) : (
                String(value ?? '')
              )}
            </Text>
          </Group>
          <Divider />
        </Stack>
      ))}
    </Stack>
  )
}
