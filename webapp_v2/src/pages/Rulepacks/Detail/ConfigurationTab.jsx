import { Box, Card, Flex, Group, Stack, Text, Title } from '@mantine/core'
import { EyeOff, Shield } from 'lucide-react'

function RuleRow({ rule, isLast }) {
  return (
    <Box
      p="lg"
      style={
        isLast
          ? undefined
          : { borderBottom: '1px solid var(--mantine-color-default-border)' }
      }
    >
      <Stack gap={4}>
        <Text size="md" fw={700} lineClamp={1}>
          {rule.name}
        </Text>
        {rule.description && (
          <Text size="sm" c="dimmed">
            {rule.description}
          </Text>
        )}
      </Stack>
    </Box>
  )
}

function Section({ icon, title, description, rules }) {
  if (!rules || rules.length === 0) return null
  const ruleLabel = rules.length === 1 ? 'rule' : 'rules'
  const Icon = icon

  return (
    <Stack gap="md">
      <Group gap="md" align="center">
        <Flex
          align="center"
          justify="center"
          w={40}
          h={40}
          bg="gray.1"
          style={{ borderRadius: 12, flexShrink: 0 }}
        >
          <Icon size={16} />
        </Flex>
        <Stack gap={0} style={{ flex: 1, minWidth: 0 }}>
          <Title order={4}>{`${title} • ${rules.length} ${ruleLabel}`}</Title>
          {description && (
            <Text size="sm" c="dimmed">
              {description}
            </Text>
          )}
        </Stack>
      </Group>
      <Card padding={0} withBorder>
        {rules.map((rule, idx) => (
          <RuleRow
            key={rule.id ?? rule.name}
            rule={rule}
            isLast={idx === rules.length - 1}
          />
        ))}
      </Card>
    </Stack>
  )
}

export default function ConfigurationTab({ rulepack }) {
  const dataMasking = rulepack?.data_masking_rules ?? []
  const guardrails = rulepack?.guardrail_rules ?? []
  const nothing = dataMasking.length === 0 && guardrails.length === 0

  return (
    <Stack gap="xl">
      {nothing && (
        <Card padding="xl" withBorder>
          <Text size="sm" c="dimmed" ta="center">
            This rulepack has no rules configured.
          </Text>
        </Card>
      )}
      <Section
        icon={EyeOff}
        title="Data Masking"
        description="Redact PII and secrets in command output before they reach the user."
        rules={dataMasking}
      />
      <Section
        icon={Shield}
        title="Guardrails"
        description="Block risky inputs and outputs before they reach the target system."
        rules={guardrails}
      />
    </Stack>
  )
}
