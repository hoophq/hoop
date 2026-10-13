import { useCallback, useState } from 'react'
import { useRuleTraffics } from '@/modes'
import { useAgentsEnabled } from '@/modes/agents'
import {
  TRAFFIC_AGENT,
  TRAFFIC_BOTH,
  TRAFFIC_SIDECAR,
  ruleTraffic,
} from '@/utils/ruleTraffic'

const LABELS = { 'Agent traffic': TRAFFIC_AGENT, 'Sidecar traffic': TRAFFIC_SIDECAR }
const VALUES = Object.keys(LABELS)

// The traffic filter of a rule list, and which of the other filters apply under
// it: resource role and attribute for agent rules, listener for sidecar rules.
export function useRuleTrafficFilter(kind) {
  const traffics = useRuleTraffics()
  const agentsEnabled = useAgentsEnabled()
  const mixed = traffics.length > 1
  // Every rule list carries sidecar_spec, so a row is classified whenever both
  // traffics exist.
  const classifiable = mixed
  const showTrafficFilter = classifiable && agentsEnabled
  const [label, setLabel] = useState(null)
  const selected = showTrafficFilter ? (LABELS[label] ?? null) : null

  const showAgentFilters =
    agentsEnabled && traffics.includes(TRAFFIC_AGENT) && selected !== TRAFFIC_SIDECAR
  const showSidecarFilter = traffics.includes(TRAFFIC_SIDECAR) && selected !== TRAFFIC_AGENT

  const matches = useCallback(
    (rule) => {
      if (!selected) return true
      const traffic = ruleTraffic(kind, rule)
      return traffic === selected || traffic === TRAFFIC_BOTH
    },
    [kind, selected],
  )

  return {
    traffics,
    mixed,
    classifiable,
    showTrafficFilter,
    showAgentFilters,
    showSidecarFilter,
    matches,
    trafficOf: (rule) => (classifiable ? ruleTraffic(kind, rule) : null),
    filterProps: {
      label: 'Traffic',
      values: VALUES,
      selected: label,
      onSelect: setLabel,
      onClear: () => setLabel(null),
    },
    active: Boolean(selected),
  }
}
