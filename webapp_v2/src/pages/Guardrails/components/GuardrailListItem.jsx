import { Box, Flex, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'

const BORDER = '1px solid var(--mantine-color-default-border)'

// One row of the guardrails list. Rows stack into a single bordered block, so
// only the first and last ones carry the outer corners.
export default function GuardrailListItem({ guardrail, isFirst, isLast, onConfigure }) {
  return (
    <Box
      style={{
        borderLeft: BORDER,
        borderRight: BORDER,
        borderTop: isFirst ? BORDER : undefined,
        borderBottom: BORDER,
        borderTopLeftRadius: isFirst ? 'var(--mantine-radius-md)' : undefined,
        borderTopRightRadius: isFirst ? 'var(--mantine-radius-md)' : undefined,
        borderBottomLeftRadius: isLast ? 'var(--mantine-radius-md)' : undefined,
        borderBottomRightRadius: isLast ? 'var(--mantine-radius-md)' : undefined,
      }}
    >
      <Flex p="lg" align="center" justify="space-between" gap="md">
        <Stack gap="xs" flex={1} miw={0}>
          <Text fw={500} fz="lg">
            {guardrail.name}
          </Text>
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
