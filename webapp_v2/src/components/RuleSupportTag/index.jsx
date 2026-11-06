import { Group } from '@mantine/core'
import Badge from '@/components/Badge'
import { AGENT_ONLY, SIDECAR_ONLY } from '@/utils/ruleSupport'

// Inline marker on a form option only one of the two engines runs. Same
// palette as RuleTrafficBadge: indigo is the sidecar, gray the agent.
export default function RuleSupportTag({ side }) {
  const sidecar = side === 'sidecar'
  return (
    <Badge tag fullLabel variant="light" color={sidecar ? 'indigo' : 'gray'} size="xs">
      {sidecar ? SIDECAR_ONLY : AGENT_ONLY}
    </Badge>
  )
}

// An input label with the marker beside it.
export function TaggedLabel({ text, side }) {
  return (
    <Group gap="xs" component="span">
      {text}
      <RuleSupportTag side={side} />
    </Group>
  )
}

// A Select option with the marker when `tagged` holds:
//   renderOption={({ option }) => <TaggedOption option={option} tagged={option.value === 'x'} side="agent" />}
export function TaggedOption({ option, tagged, side }) {
  return (
    <Group gap="xs" justify="space-between" w="100%" wrap="nowrap">
      {option.label}
      {tagged && <RuleSupportTag side={side} />}
    </Group>
  )
}
