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
          color={selected ? 'indigo' : 'gray'}
          radius="var(--hoop-icon-radius)"
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
