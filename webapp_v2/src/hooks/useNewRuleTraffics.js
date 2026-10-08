import { useEffect } from 'react'
import { useRuleTraffics } from '@/modes'
import { useAgentsEnabled } from '@/modes/agents'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { TRAFFIC_AGENT, TRAFFIC_SIDECAR } from '@/utils/ruleTraffic'

const NONE = []
const AGENT_ONLY = [TRAFFIC_AGENT]
const SIDECAR_ONLY = [TRAFFIC_SIDECAR]
const AGENT_AND_SIDECAR = [TRAFFIC_AGENT, TRAFFIC_SIDECAR]

// The traffic a new rule can protect. An agents org gets the sidecar choice
// only once it has a sidecar. Editing an existing rule reads useRuleTraffics().
export function useNewRuleTraffics() {
  const traffics = useRuleTraffics()
  const agentsEnabled = useAgentsEnabled()
  const sidecarCount = useSidecarStore((s) => s.sidecars.length)
  const sidecarsLoading = useSidecarStore((s) => s.loading)
  const sidecarsFailed = useSidecarStore((s) => s.error !== null)
  const fetchSidecars = useSidecarStore((s) => s.fetchSidecars)
  const countsSidecars = agentsEnabled && traffics.length > 1

  useEffect(() => {
    if (countsSidecars) fetchSidecars()
  }, [countsSidecars, fetchSidecars])

  if (traffics.length === 1) return traffics
  if (!agentsEnabled) return SIDECAR_ONLY
  // A failed count keeps both choices; a pending one offers none yet.
  if (sidecarCount > 0 || sidecarsFailed) return AGENT_AND_SIDECAR
  return sidecarsLoading ? NONE : AGENT_ONLY
}
