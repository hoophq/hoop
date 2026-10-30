import { Group, Text } from '@mantine/core'

// One detail of the session (Figma "Information Module"): an icon and a
// 160px label, then the value.
export default function InfoRow({ icon: Icon, label, children }) {
  return (
    <Group gap="smAlt" wrap="nowrap" mih={32}>
      <Group gap="smAlt" w={160} wrap="nowrap" flex="none">
        {Icon && <Icon size={20} color="var(--mantine-color-dimmed)" aria-hidden />}
        <Text size="sm" c="dimmed" truncate>
          {`${label}:`}
        </Text>
      </Group>
      {children}
    </Group>
  )
}
