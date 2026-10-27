import { Container, Server, Waypoints } from 'lucide-react'
import Badge from '@/components/Badge'
import { TRAFFIC_AGENT, TRAFFIC_BOTH, TRAFFIC_SIDECAR } from '@/utils/ruleTraffic'

const TRAFFIC_BADGES = {
  [TRAFFIC_AGENT]: { label: 'Agent', color: 'gray', Icon: Server },
  [TRAFFIC_SIDECAR]: { label: 'Sidecar', color: 'indigo', Icon: Container },
  [TRAFFIC_BOTH]: { label: 'Agent and sidecar', color: 'amber', Icon: Waypoints },
}

export default function RuleTrafficBadge({ traffic }) {
  const { label, color, Icon } = TRAFFIC_BADGES[traffic]
  return (
    <Badge tag chip variant="light" color={color} icon={<Icon size={12} aria-hidden="true" />}>
      {label}
    </Badge>
  )
}
