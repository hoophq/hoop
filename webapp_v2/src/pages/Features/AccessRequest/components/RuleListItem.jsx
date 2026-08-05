import { Flex, Stack, Text } from '@mantine/core'
import { ChevronRight } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import classes from './RuleListItem.module.css'

// One row of the access request list. Rows stack into a single bordered block,
// so only the first and last ones carry the outer corners.
export default function RuleListItem({ rule, isFirst, isLast, onConfigure }) {
  return (
    <Flex
      className={classes.row}
      data-first={isFirst || undefined}
      data-last={isLast || undefined}
      p="lg"
      mih={106}
      align="center"
      justify="space-between"
      gap="md"
    >
      <Stack gap="xs" miw={0}>
        <Text fw={500} fz="lg">
          {rule.name}
        </Text>
        {rule.description && (
          <Text size="sm" c="dimmed">
            {rule.description}
          </Text>
        )}
      </Stack>

      <ActionIcon
        variant="subtle"
        color="gray"
        size="lg"
        aria-label={`Edit ${rule.name}`}
        onClick={() => onConfigure(rule.name)}
      >
        <ChevronRight size={24} />
      </ActionIcon>
    </Flex>
  )
}
