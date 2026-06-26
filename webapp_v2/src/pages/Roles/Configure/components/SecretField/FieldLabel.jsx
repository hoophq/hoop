import { Group, Text } from '@mantine/core'

// Field label with the optional required asterisk. Used by the SecretField
// states that render their own label above a multiline control, where the
// input's built-in `label` prop isn't available.
export default function FieldLabel({ label, required }) {
  if (!label) return null
  return (
    <Group gap={4}>
      <Text size="sm" fw={500}>
        {label}
      </Text>
      {required && (
        <Text size="sm" c="red">
          *
        </Text>
      )}
    </Group>
  )
}
