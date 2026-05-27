import { Group, Stack, Title, Text } from '@mantine/core'
import Switch from '@/components/Switch'

// Reusable toggle row used across Terminal Access and Native Access.
// Mirrors the CLJS toggle-section pattern: a Switch on the left, a
// titled description on the right, and an optional `complement` slot
// that renders only when the toggle is on.
export default function ToggleSection({
  title,
  description,
  checked,
  disabled,
  onChange,
  complement,
  learnMore,
}) {
  return (
    <Group align="flex-start" gap="md" wrap="nowrap">
      <Switch
        size="md"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.currentTarget.checked)}
        aria-label={title}
      />
      <Stack gap="xs" flex={1}>
        <Title order={5} fw={500}>{title}</Title>
        <Text size="sm" c="dimmed">{description}</Text>
        {complement}
        {learnMore}
      </Stack>
    </Group>
  )
}
