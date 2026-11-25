import { Box, Flex, Group, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import RuleTrafficBadge from '@/components/RuleTrafficBadge'

// One row of the guardrails list. Rows stack into a single bordered block, so
// only the first and last ones carry the outer corners.
export default function GuardrailListItem({ guardrail, traffic, onConfigure }) {
  return (
    <Box>
      <Flex p="lg" align="center" justify="space-between" gap="md">
        <Stack gap="xs" flex={1} miw={0}>
          <Group gap="sm">
            <Text fw={500} fz="lg">
              {guardrail.name}
            </Text>
            {traffic && <RuleTrafficBadge traffic={traffic} />}
          </Group>
          {guardrail.description && (
            <Text size="sm" c="dimmed">
              {guardrail.description}
            </Text>
          )}
        </Stack>
        {/* Hoop-managed guardrails (protection profiles) are immutable — the
            API rejects updates, so there is nothing to configure. */}
        {!guardrail.managed_by && (
          <Button variant="default" onClick={() => onConfigure(guardrail.id)}>
            Configure
          </Button>
        )}
      </Flex>
    </Box>
  )
}
