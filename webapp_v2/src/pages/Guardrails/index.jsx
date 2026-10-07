import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { Info, ListVideo, Network, Rotate3d } from 'lucide-react'
import Alert from '@/components/Alert'
import AsyncValueFilter from '@/components/AsyncValueFilter'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import NewRuleButton from '@/components/NewRuleButton'
import PageLoader from '@/components/PageLoader'
import SidecarListenerFilter from '@/components/SidecarListenerFilter'
import ValueFilter from '@/components/ValueFilter'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import { useRuleTrafficFilter } from '@/hooks/useRuleTrafficFilter'
import EmptyState from '@/layout/EmptyState'
import FullBleed from '@/layout/FullBleed'
import { boundRuleNames } from '@/pages/Sidecars/config'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { useUserStore } from '@/stores/useUserStore'
import {
  RULE_KIND_GUARDRAIL,
  TRAFFIC_AGENT,
  TRAFFIC_SIDECAR,
  newRulePath,
} from '@/utils/ruleTraffic'
import { useGuardrailsStore } from './store'
import GuardrailListItem from './components/GuardrailListItem'
import GuardrailsPromotion from './components/GuardrailsPromotion'

const FREE_LICENSE_LIMIT_MESSAGE =
  'Your organization has reached Guardrails free usage limits. Upgrade to Enterprise to keep your sensitive data protected.'

const AGENT_DLP_REQUIRED =
  'Guardrails for agent resources need a DLP provider (Microsoft Presidio or Google Cloud DLP). Sidecars enforce guardrails without one.'

function uniqueSorted(values) {
  return [...new Set(values)].sort((a, b) => a.localeCompare(b))
}

// Sidecars enforce guardrails without a DLP provider, and a sidecar rule binds
// to listeners, not resource roles or attributes.
export default function Guardrails() {
  const navigate = useNavigate()

  const list = useGuardrailsStore((s) => s.list)
  const listStatus = useGuardrailsStore((s) => s.listStatus)
  const attributes = useGuardrailsStore((s) => s.attributes)
  const fetchList = useGuardrailsStore((s) => s.fetchList)
  const fetchAttributes = useGuardrailsStore((s) => s.fetchAttributes)
  const sidecars = useSidecarStore((s) => s.sidecars)

  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  // A DLP provider (gcp or mspresidio) is required to enforce guardrails;
  // has_redact_credentials is true only when one of those is configured.
  const hasRedactCredentials = useUserStore((s) => s.hasRedactCredentials)

  const traffic = useRuleTrafficFilter(RULE_KIND_GUARDRAIL)
  const { traffics, mixed, showAgentFilters, showSidecarFilter, matches } = traffic
  const agentRules = traffics.includes(TRAFFIC_AGENT)
  const sidecarRules = traffics.includes(TRAFFIC_SIDECAR)
  const agentBlocked = agentRules && !hasRedactCredentials

  const [selectedRole, setSelectedRole] = useState(null)
  const [selectedAttribute, setSelectedAttribute] = useState(null)
  const [selectedTarget, setSelectedTarget] = useState(null)

  const roleFilter = usePaginatedConnections({ pageSize: 50 })

  useEffect(() => {
    fetchList()
    if (agentRules) fetchAttributes()
  }, [fetchList, fetchAttributes, agentRules])

  const attributeFilterValues = useMemo(
    () => uniqueSorted(attributes.map((a) => a.name)),
    [attributes],
  )

  const target = showSidecarFilter ? selectedTarget : null
  const role = showAgentFilters ? selectedRole : null
  const attribute = showAgentFilters ? selectedAttribute : null

  const filteredGuardrails = useMemo(() => {
    let guardrails = list.filter(matches)
    if (target) {
      const names = boundRuleNames(sidecars, target, 'guardrail')
      guardrails = guardrails.filter((guardrail) => names.has(guardrail.name))
    }
    if (role) {
      guardrails = guardrails.filter((guardrail) =>
        (guardrail.connection_ids ?? []).includes(role.value),
      )
    }
    if (attribute) {
      guardrails = guardrails.filter((guardrail) =>
        (guardrail.attributes ?? []).includes(attribute),
      )
    }
    return guardrails
  }, [list, matches, sidecars, target, role, attribute])

  const atFreeLimit = isFreeLicense && list.length >= 1
  const loading = listStatus === 'loading'
  const showLoader = useMinDelay(loading && list.length === 0, 500)
  const activeFilterCount =
    (role ? 1 : 0) + (attribute ? 1 : 0) + (target ? 1 : 0) + (traffic.active ? 1 : 0)

  const goCreate = (kind) => navigate(newRulePath('/guardrails/new', traffics, kind))

  if (showLoader) {
    return <PageLoader h={300} />
  }

  // A failed load leaves the list empty, which would otherwise fall through to
  // the empty state and tell an admin they have no guardrails configured.
  if (listStatus === 'error') {
    return <PageLoader error h={300} message="Failed to load guardrails." />
  }

  // Without a DLP provider agent guardrails cannot be enforced, so the
  // requirement screen replaces the list even when guardrails already exist.
  // It never hides sidecar rules.
  if (!sidecarRules && !hasRedactCredentials) {
    return (
      <FullBleed>
        <GuardrailsPromotion dlpAvailable={false} onCreate={() => goCreate(traffics[0])} />
      </FullBleed>
    )
  }

  if (list.length === 0 && !mixed) {
    return (
      <FullBleed>
        <GuardrailsPromotion dlpAvailable onCreate={() => goCreate(traffics[0])} />
      </FullBleed>
    )
  }

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start">
        <Stack gap="sm">
          <Title order={1}>Guardrails</Title>
          <Text size="md" c="dimmed">
            Create custom rules to guide and protect usage within your resource roles
          </Text>
        </Stack>
        <NewRuleButton
          traffics={traffics}
          onCreate={goCreate}
          disabled={atFreeLimit}
          blocked={agentBlocked ? { [TRAFFIC_AGENT]: 'Needs a DLP provider' } : undefined}
        >
          Create new rule
        </NewRuleButton>
      </Group>

      {atFreeLimit && (
        <FreeLicenseCallout message={FREE_LICENSE_LIMIT_MESSAGE} variant="limit" />
      )}

      {mixed && agentBlocked && (
        <Alert color="gray" variant="light" icon={<Info size={16} />}>
          {AGENT_DLP_REQUIRED}
        </Alert>
      )}

      {list.length > 0 && (
        <Group gap="sm">
          {traffic.classifiable && <ValueFilter icon={Network} {...traffic.filterProps} />}
          {showSidecarFilter && (
            <SidecarListenerFilter
              selected={selectedTarget}
              onSelect={setSelectedTarget}
              onClear={() => setSelectedTarget(null)}
            />
          )}
          {showAgentFilters && (
            <>
              <AsyncValueFilter
                icon={Rotate3d}
                label="Resource Role"
                placeholder="Search resource roles"
                selected={selectedRole}
                onSelect={setSelectedRole}
                onClear={() => setSelectedRole(null)}
                options={roleFilter.options}
                loading={roleFilter.loading}
                hasMore={roleFilter.hasMore}
                onLoadMore={roleFilter.loadMore}
                searchValue={roleFilter.searchValue}
                onSearchChange={roleFilter.setSearch}
                onOpen={roleFilter.ensureLoaded}
              />
              <ValueFilter
                icon={ListVideo}
                label="Attribute"
                values={attributeFilterValues}
                selected={selectedAttribute}
                onSelect={setSelectedAttribute}
                onClear={() => setSelectedAttribute(null)}
              />
            </>
          )}
        </Group>
      )}

      {list.length === 0 ? (
        <EmptyState
          compact
          title="No Guardrails yet"
          description="Create one for agent resources or for sidecar listeners."
        />
      ) : filteredGuardrails.length === 0 ? (
        <EmptyState
          compact
          title="No Guardrails match your filters"
          description={`Try clearing the ${activeFilterCount > 1 ? 'filters' : 'filter'} above.`}
        />
      ) : (
        <Box>
          {filteredGuardrails.map((guardrail, idx) => (
            <GuardrailListItem
              key={guardrail.id}
              guardrail={guardrail}
              traffic={traffic.trafficOf(guardrail)}
              isFirst={idx === 0}
              isLast={idx === filteredGuardrails.length - 1}
              onConfigure={(id) => navigate(`/guardrails/edit/${id}`)}
            />
          ))}
        </Box>
      )}
    </Stack>
  )
}
