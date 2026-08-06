import { Box, Stack, Text, Title } from '@mantine/core'
import { Check, Info, ShieldCheck, X } from 'lucide-react'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import SelectionCard from '@/components/SelectionCard'
import Select from '@/components/Select'
import { REQUIRE_ACCESS_REQUEST, RISK_LEVELS } from '../../helpers'
import SectionRow from '../../components/SectionRow'

const ACTIONS = [
  {
    value: 'allow_execution',
    icon: Check,
    title: 'Allow execution',
    description: 'User activity will proceed normally.',
  },
  {
    value: 'block_execution',
    icon: X,
    title: 'Block execution',
    description: 'User activity will be blocked.',
  },
  {
    value: REQUIRE_ACCESS_REQUEST,
    icon: ShieldCheck,
    title: 'Require access request',
    description:
      'User activity will wait for an access request approval before running.',
  },
]

const RECOMMENDED_BADGE = (
  <Badge variant="light" color="indigo">
    Recommended
  </Badge>
)

function RiskLevelSection({ level, tier, onChange, accessRequestRules }) {
  const ruleOptions = accessRequestRules.map((rule) => ({
    value: rule.name,
    label: rule.name,
  }))

  return (
    <SectionRow title={level.label} description={level.description}>
      <Stack gap="sm">
        {ACTIONS.map((action) => (
          <SelectionCard
            key={action.value}
            icon={action.icon}
            title={action.title}
            description={action.description}
            badge={level.recommended === action.value ? RECOMMENDED_BADGE : null}
            selected={tier.action === action.value}
            // Only an approval gate carries a rule, so leaving that action
            // drops the previous selection instead of saving it invisibly.
            onClick={() =>
              onChange({
                action: action.value,
                ruleName: action.value === REQUIRE_ACCESS_REQUEST ? tier.ruleName : null,
              })
            }
          />
        ))}

        {tier.action === REQUIRE_ACCESS_REQUEST && (
          <Box pl="md">
            {ruleOptions.length === 0 ? (
              <Alert color="yellow" variant="light" icon={<Info size={16} />} radius="md">
                No access request rules are configured. Create one in Access Control
                before selecting this action.
              </Alert>
            ) : (
              <Select
                label="Access request rule"
                placeholder="Select an access request rule"
                data={ruleOptions}
                value={tier.ruleName}
                onChange={(value) => onChange({ action: tier.action, ruleName: value })}
                required
              />
            )}
          </Box>
        )}
      </Stack>
    </SectionRow>
  )
}

// The three tiers the analyzer grades a session into, each mapped to the action
// taken at session time.
export default function RiskEvaluation({ risk, onTierChange, accessRequestRules }) {
  return (
    <Stack gap="xxlAlt">
      {/* The section heading spans the full width — unlike the rest of the
          form, it introduces the three rows below it rather than labelling a
          field column. */}
      <Stack gap="xs">
        <Title order={4} fw={500}>
          Risk evaluation
        </Title>
        <Text size="sm" c="dimmed">
          Define policies by risk level and define actions at session time.
        </Text>
        <Alert color="indigo" variant="light" icon={<Info size={16} />} radius="md" mt="xs">
          {"Recommended policies are calibrated to the session's risk profile"}
        </Alert>
      </Stack>

      {RISK_LEVELS.map((level) => (
        <RiskLevelSection
          key={level.key}
          level={level}
          tier={risk[level.key]}
          onChange={(tier) => onTierChange(level.key, tier)}
          accessRequestRules={accessRequestRules}
        />
      ))}
    </Stack>
  )
}
