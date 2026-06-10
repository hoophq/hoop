import { UnstyledButton, Group, Stack, Text } from '@mantine/core'
import { ChevronRight } from 'lucide-react'

/**
 * Pressable list row for mobile screens. Compose inside a
 * `<Card padding={0} withBorder>` with `<Divider />` between rows.
 */
function MobileListCard({ title, subtitle, meta, rightSection, onClick }) {
  return (
    <UnstyledButton onClick={onClick} w="100%" p="md">
      <Group justify="space-between" wrap="nowrap" gap="sm">
        <Stack gap={2} miw={0} flex={1}>
          <Text fw={600} size="sm" truncate="end">
            {title}
          </Text>
          {subtitle && (
            <Text size="xs" c="dimmed" truncate="end">
              {subtitle}
            </Text>
          )}
          {meta && (
            <Text size="xs" c="dimmed">
              {meta}
            </Text>
          )}
        </Stack>
        <Group gap="xs" wrap="nowrap">
          {rightSection}
          <ChevronRight size={16} color="var(--mantine-color-dimmed)" />
        </Group>
      </Group>
    </UnstyledButton>
  )
}

export default MobileListCard
