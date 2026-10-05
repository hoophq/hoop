import { useEffect, useState } from 'react'
import { useParams, useSearchParams } from 'react-router-dom'
import { Text } from '@mantine/core'
import PageLoader from '@/components/PageLoader'
import {
  TRAFFIC_AGENT,
  TRAFFIC_BOTH,
  TRAFFIC_SIDECAR,
  resolveRuleTraffic,
} from '@/utils/ruleTraffic'
import { useRuleTraffics } from './index'

/**
 * Picks the agent or the sidecar form of a guardrail, masking or analyzer rule.
 * Used in Router.jsx only, like ByProduct:
 *
 *   <ByRuleTraffic kind="guardrail" fetchRule={guardrailsService.get}
 *     agent={<GatewayGuardrailForm />} sidecar={<ControlPlaneGuardrailForm />} />
 *
 * With one traffic available it renders that form. With both, an edit reads the
 * rule first and a new rule takes `?traffic=sidecar`.
 */
export default function ByRuleTraffic({ kind, fetchRule, param = 'id', agent, sidecar }) {
  const traffics = useRuleTraffics()
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
    const asked = searchParams.get('traffic') === TRAFFIC_SIDECAR ? TRAFFIC_SIDECAR : TRAFFIC_AGENT
    const traffic = traffics.length === 1 ? traffics[0] : asked
    return traffic === TRAFFIC_SIDECAR ? sidecar : agent
  }
  if (probe?.key !== key) return <PageLoader h={400} />
  if (probe.failed) return <Text c="red">Failed to load the rule.</Text>
  // The sidecar form saves empty agent fields, so it would erase the agent half.
  if (probe.traffic === TRAFFIC_BOTH) {
    return (
      <Text c="red">
        This rule protects agent and sidecar traffic, and this page cannot edit both yet.
      </Text>
    )
  }
  return probe.traffic === TRAFFIC_SIDECAR ? sidecar : agent
}
