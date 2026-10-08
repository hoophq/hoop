import { useEffect, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import {
  TRAFFIC_AGENT,
  TRAFFIC_BOTH,
  TRAFFIC_SIDECAR,
  resolveRuleTraffic,
} from '@/utils/ruleTraffic'
import { useAgentsEnabled } from './agents'
import { useRuleTraffics } from './index'

const NEW_RULE_TRAFFICS = [TRAFFIC_AGENT, TRAFFIC_SIDECAR]

/**
 * Picks the agent or the sidecar form of a guardrail, masking or analyzer rule.
 * Used in Router.jsx only, like ByProduct:
 *
 *   <ByRuleTraffic kind="guardrail" fetchRule={guardrailsService.get} listPath="/guardrails"
 *     agent={<GatewayGuardrailForm />} sidecar={<ControlPlaneGuardrailForm />} />
 *
 * With one traffic available it renders that form. With both, an edit reads the
 * rule first and a new rule takes `?traffic=`; without it, the agents flag picks.
 */
export default function ByRuleTraffic({ kind, fetchRule, listPath, param = 'id', agent, sidecar }) {
  const navigate = useNavigate()
  const traffics = useRuleTraffics()
  const agentsEnabled = useAgentsEnabled()
  const key = useParams()[param]
  const [searchParams] = useSearchParams()
  const probeNeeded = traffics.length > 1 && Boolean(key)
  const [probe, setProbe] = useState(null)

  useEffect(() => {
    if (!probeNeeded) return undefined
    let current = true
    fetchRule(key).then(
      ({ data }) => current && setProbe({ key, traffic: resolveRuleTraffic(traffics, kind, data) }),
      () => current && setProbe({ key, failed: true }),
    )
    return () => {
      current = false
    }
  }, [probeNeeded, key, fetchRule, traffics, kind])

  if (!probeNeeded) {
    const asked = searchParams.get('traffic')
    const fallback = agentsEnabled ? TRAFFIC_AGENT : TRAFFIC_SIDECAR
    const traffic = traffics.length === 1 ? traffics[0] : NEW_RULE_TRAFFICS.includes(asked) ? asked : fallback
    return traffic === TRAFFIC_SIDECAR ? sidecar : agent
  }
  if (probe?.key !== key) return <PageLoader h={400} />
  if (probe.failed) return <PageLoader error h={400} message="Failed to load the rule." />
  // The sidecar form saves empty agent fields, so it would erase the agent half.
  if (probe.traffic === TRAFFIC_BOTH) {
    return (
      <EmptyState
        title="This rule protects agent and sidecar traffic"
        description="This page edits one kind of traffic per rule, so it cannot open this rule yet. The rule keeps running unchanged."
        action={{ label: 'Back to rules', onClick: () => navigate(listPath) }}
      />
    )
  }
  return probe.traffic === TRAFFIC_SIDECAR ? sidecar : agent
}
