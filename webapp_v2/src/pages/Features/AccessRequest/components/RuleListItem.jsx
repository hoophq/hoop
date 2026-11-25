import { Box, Flex, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'

// One row of the access request list. Rows stack into a single bordered block,
// so only the first and last ones carry the outer corners.
export default function RuleListItem({ rule, onConfigure }) {
  return (
    <Box>
      <Flex p="lg" align="center" justify="space-between" gap="md">
        <Stack gap="xs" flex={1} miw={0}>
          <Text fw={500} fz="lg">
            {rule.name}
          </Text>
          {rule.description && (
            <Text size="sm" c="dimmed">
              {rule.description}
            </Text>
          )}
        </Stack>
        {/* Hoop-managed rules stay configurable: the API locks their name,
            access type, duration and targeting, but the approval settings
            are still editable. */}
        <Button variant="default" onClick={() => onConfigure(rule.name)}>
          Configure
        </Button>
      </Flex>
    </Box>
  )
}
