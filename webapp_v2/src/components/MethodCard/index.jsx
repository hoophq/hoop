import { Box, Flex, Text } from '@mantine/core'
import Avatar from '@/components/Avatar'
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
          color={selected ? 'indigo' : 'gray'}
          tile
          src={method.iconDark}
          alt={method.label}
        />
        <Box>
          <Text size="sm" fw={500}>
            {method.label}
          </Text>
          <Text size="xs" c="dimmed">
            {method.description}
          </Text>
        </Box>
      </Flex>
    </Box>
  )
}

export default MethodCard
