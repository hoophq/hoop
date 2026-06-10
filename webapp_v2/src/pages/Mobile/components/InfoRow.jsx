import { Group, Text } from '@mantine/core'

/**
 * Key-value row for mobile detail screens. Pass `value` for plain text or
 * `children` for custom nodes (badges, groups). Compose inside a
 * `<Card padding={0} withBorder>` with `<Divider />` between rows.
 */
function InfoRow({ label, value, children }) {
  return (
    <Group justify="space-between" align="center" wrap="nowrap" px="md" py="sm" gap="md">
      <Text size="sm" c="dimmed" flex="0 0 auto">
        {label}
      </Text>
      {children ?? (
        <Text size="sm" ta="right" truncate="end">
          {value ?? '—'}
        </Text>
      )}
    </Group>
  )
}

export default InfoRow
