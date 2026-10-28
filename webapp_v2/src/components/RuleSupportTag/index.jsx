import Badge from '@/components/Badge'
import { AGENT_ONLY, SIDECAR_ONLY } from '@/utils/ruleSupport'

// Inline marker on a form option only one of the two engines runs. Same
// palette as RuleTrafficBadge: indigo is the sidecar, gray the agent.
export default function RuleSupportTag({ side }) {
  const sidecar = side === 'sidecar'
  return (
    <Badge tag variant="light" color={sidecar ? 'indigo' : 'gray'} size="xs">
      {sidecar ? SIDECAR_ONLY : AGENT_ONLY}
    </Badge>
  )
}
