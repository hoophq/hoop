import { UnstyledButton, Stack, Text } from '@mantine/core'
import { useNavigate } from 'react-router-dom'

/**
 * Bottom tab bar item. Color cascades to the lucide icon via currentColor,
 * so a single `c` prop drives both icon and label.
 */
function MobileTabLink({ tab, active }) {
  const navigate = useNavigate()

  return (
    <UnstyledButton
      onClick={() => navigate(tab.path)}
      h="100%"
      c={active ? 'indigo.8' : 'dimmed'}
      aria-label={tab.label}
      aria-current={active ? 'page' : undefined}
    >
      <Stack align="center" justify="center" gap={2} h="100%">
        <tab.icon size={22} strokeWidth={active ? 2.2 : 1.8} aria-hidden="true" />
        <Text size="xs" fw={active ? 600 : 500} c="inherit">
          {tab.label}
        </Text>
      </Stack>
    </UnstyledButton>
  )
}

export default MobileTabLink
