import { Group, Burger, Text } from '@mantine/core'
import { useUIStore } from '@/stores/useUIStore'

function Header() {
  const { sidebarOpen, toggleSidebar } = useUIStore()

  return (
    <Group h="100%" px="md" justify="space-between">
      <Group>
        <Burger opened={sidebarOpen} onClick={toggleSidebar} size="sm" />
        <Text size="sm" c="dimmed">
          Hoop
        </Text>
      </Group>
    </Group>
  )
}

export default Header
