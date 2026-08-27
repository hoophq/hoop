import { Box, Flex, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import classes from './RuleListItem.module.css'

// One row of the rules list. Rows stack into a single bordered block, so only
// the first and last ones carry the outer corners.
export default function RuleListItem({ rule, isFirst, isLast, onConfigure }) {
  return (
    <Box
      className={classes.row}
      data-first={isFirst || undefined}
      data-last={isLast || undefined}
    >
      <Flex p="lg" align="center" justify="space-between" gap="md">
        <Stack gap="xs" flex={1} miw={0}>
          <Text fw={500} fz="lg">
            {rule.name || 'Unnamed Rule'}
          </Text>
          {rule.description && (
            <Text size="sm" c="dimmed">
              {rule.description}
            </Text>
          )}
        </Stack>
        {/* Hoop-managed rules (protection profiles) are immutable — the API
            rejects updates, so there is nothing to configure. */}
        {!rule.managed_by && (
          <Button variant="default" onClick={() => onConfigure(rule.name)}>
            Configure
          </Button>
        )}
      </Flex>
    </Box>
  )
}
