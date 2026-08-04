import { Group, ThemeIcon, Title } from '@mantine/core'
import { Cable, X } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import { DRAWER_TITLE_ID } from './constants'

export function DrawerHeader({ onClose }) {
  return (
    <Group justify="space-between" wrap="nowrap" p="md">
      <Group gap="sm" wrap="nowrap">
        <ThemeIcon variant="light" color="gray" size={32} radius="md">
          <Cable size={16} aria-hidden="true" />
        </ThemeIcon>
        <Title order={4} id={DRAWER_TITLE_ID}>
          Native connections
        </Title>
      </Group>
      <ActionIcon variant="subtle" color="gray" onClick={onClose} aria-label="Close drawer">
        <X size={20} aria-hidden="true" />
      </ActionIcon>
    </Group>
  )
}
