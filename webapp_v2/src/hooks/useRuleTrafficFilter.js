import { useCallback, useState } from 'react'
import { useRuleTraffics } from '@/modes'
import {
  LIST_CARRIES_SIDECAR_SPEC,
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
  const mixed = traffics.length > 1
  const classifiable = mixed && LIST_CARRIES_SIDECAR_SPEC[kind]
  const [label, setLabel] = useState(null)
  const selected = classifiable ? (LABELS[label] ?? null) : null

  const showAgentFilters = traffics.includes(TRAFFIC_AGENT) && selected !== TRAFFIC_SIDECAR
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
