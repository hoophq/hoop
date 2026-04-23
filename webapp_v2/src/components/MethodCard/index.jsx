import { Box, Flex, Avatar, Text } from '@mantine/core'
import classes from './MethodCard.module.css'

function MethodCard({ method, selected, onSelect }) {
  return (
    <Box
      p="sm"
      onClick={() => onSelect(method.id)}
      className={classes.card}
      data-selected={selected || undefined}
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
