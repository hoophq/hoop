import { Box, Flex, Avatar, Text } from '@mantine/core'

/**
 * Selection card for setup wizards (agents, connections, etc.)
 *
 * method: { id, label, description, iconDark, iconLight }
 * selected: boolean
 * onSelect: (id) => void
 */
function MethodCard({ method, selected, onSelect }) {
  return (
    <Box
      p="sm"
      onClick={() => onSelect(method.id)}
      style={{
        border: '1px solid var(--mantine-color-default-border)',
        borderRadius: 12,
        cursor: 'pointer',
        backgroundColor: selected ? '#182449' : 'transparent',
        transition: 'background-color 120ms ease',
      }}
    >
      <Flex gap="sm" align="center">
        <Avatar
          size="md"
          variant="soft"
          color={selected ? 'blue' : 'gray'}
          radius="sm"
          src={selected ? method.iconLight : method.iconDark}
          alt={method.label}
        />
        <Box>
          <Text size="sm" fw={500} c={selected ? 'white' : undefined}>
            {method.label}
          </Text>
          <Text size="xs" c={selected ? 'rgba(255,255,255,0.7)' : 'dimmed'}>
            {method.description}
          </Text>
        </Box>
      </Flex>
    </Box>
  )
}

export default MethodCard
