import { Box, Card, Group, Stack, Text, Title, ThemeIcon } from '@mantine/core'
import { EyeOff, Shield } from 'lucide-react'
import List from '@/components/List'

function RuleRow({ rule }) {
  return (
    <Box p="lg">
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
        <ThemeIcon variant="light" color="gray" size={40}>
          <Icon size={16} />
        </ThemeIcon>
        <Stack gap={0} flex={1} miw={0}>
          <Title order={4}>{`${title} • ${rules.length} ${ruleLabel}`}</Title>
          {description && (
            <Text size="sm" c="dimmed">
              {description}
            </Text>
          )}
        </Stack>
      </Group>
      <List>
        {rules.map((rule) => (
          <RuleRow key={rule.id ?? rule.name} rule={rule} />
        ))}
      </List>
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
