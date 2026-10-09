import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { Info, ListVideo, Network, Rotate3d } from 'lucide-react'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { useUserStore } from '@/stores/useUserStore'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import { useNewRuleTraffics } from '@/hooks/useNewRuleTraffics'
import { useRuleTrafficFilter } from '@/hooks/useRuleTrafficFilter'
import EmptyState from '@/layout/EmptyState'
import FullBleed from '@/layout/FullBleed'
import Alert from '@/components/Alert'
import PageLoader from '@/components/PageLoader'
import NewRuleButton from '@/components/NewRuleButton'
import ValueFilter from '@/components/ValueFilter'
import AsyncValueFilter from '@/components/AsyncValueFilter'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import SidecarListenerFilter from '@/components/SidecarListenerFilter'
import { boundRuleNames } from '@/pages/Sidecars/config'
import {
  RULE_KIND_DATAMASKING,
  TRAFFIC_AGENT,
  TRAFFIC_SIDECAR,
  newRuleHint,
  newRulePath,
} from '@/utils/ruleTraffic'
import { useDataMaskingStore } from './store'
import RuleListItem from './components/RuleListItem'
import DataMaskingPromotion from './components/DataMaskingPromotion'
import { RULE_DRIVEN_PROVIDERS } from './helpers'

const FREE_LICENSE_LIMIT_MESSAGE =
  'Your organization has reached Live Data Masking free usage limits. Upgrade to Enterprise to keep your sensitive data protected.'

const AGENT_PROVIDER_REQUIRED =
  'Masking rules for agent resources need a DLP provider. Sidecars mask without one.'

function uniqueSorted(values) {
  return [...new Set(values)].sort((a, b) => a.localeCompare(b))
}

// Sidecars mask without a DLP provider, and a sidecar rule binds to listeners,
// not resource roles or attributes.
export default function DataMasking() {
  const navigate = useNavigate()

  const list = useDataMaskingStore((s) => s.list)
  const listStatus = useDataMaskingStore((s) => s.listStatus)
  const attributes = useDataMaskingStore((s) => s.attributes)
  const fetchList = useDataMaskingStore((s) => s.fetchList)
  const fetchAttributes = useDataMaskingStore((s) => s.fetchAttributes)
  const sidecars = useSidecarStore((s) => s.sidecars)

  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const redactProvider = useUserStore((s) => s.redactProvider)

  const traffic = useRuleTrafficFilter(RULE_KIND_DATAMASKING)
  const { traffics, mixed, showAgentFilters, showSidecarFilter, matches } = traffic
  const agentRules = traffics.includes(TRAFFIC_AGENT)
  const sidecarRules = traffics.includes(TRAFFIC_SIDECAR)
  const newTraffics = useNewRuleTraffics()
  const agentBlocked = agentRules && !RULE_DRIVEN_PROVIDERS.includes(redactProvider)

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

  const filteredRules = useMemo(() => {
    let rules = list.filter(matches)
    if (target) {
      const names = boundRuleNames(sidecars, target, 'datamasking')
      rules = rules.filter((rule) => names.has(rule.name))
    }
    if (role) {
      rules = rules.filter((rule) => (rule.connection_ids ?? []).includes(role.value))
    }
    if (attribute) {
      rules = rules.filter((rule) => (rule.attributes ?? []).includes(attribute))
    }
    return rules
  }, [list, matches, sidecars, target, role, attribute])

  const atFreeLimit = isFreeLicense && list.length >= 1
  const loading = listStatus === 'loading'
  const showLoader = useMinDelay(loading && list.length === 0, 500)
  const activeFilterCount =
    (role ? 1 : 0) + (attribute ? 1 : 0) + (target ? 1 : 0) + (traffic.active ? 1 : 0)

  const goCreate = (kind) => navigate(newRulePath('/features/data-masking/new', kind))

  if (showLoader) {
    return <PageLoader h={300} />
  }

  // A failed load leaves the list empty, which would otherwise fall through to
  // the empty state and tell an admin they have no rules configured.
  if (listStatus === 'error') {
    return (
      <PageLoader error h={300} message="Failed to load Live Data Masking rules." />
    )
  }

  if (list.length === 0 && !mixed) {
    return (
      <FullBleed>
        <DataMaskingPromotion
          redactProvider={redactProvider}
          providerRequired={!sidecarRules}
          onConfigure={() => goCreate(newTraffics[0])}
        />
      </FullBleed>
    )
  }

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start">
        <Stack gap="sm">
          <Title order={1}>Live Data Masking</Title>
          <Text size="md" c="dimmed">
            Automatically mask sensitive data in real-time at the protocol layer
          </Text>
        </Stack>
        <NewRuleButton
          traffics={newTraffics}
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
          {AGENT_PROVIDER_REQUIRED}
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
          title="No Live Data Masking rules yet"
          description={newRuleHint(newTraffics)}
        />
      ) : filteredRules.length === 0 ? (
        <EmptyState
          compact
          title="No Live Data Masking rules match your filters"
          description={`Try clearing the ${activeFilterCount > 1 ? 'filters' : 'filter'} above.`}
        />
      ) : (
        <Box>
          {filteredRules.map((rule, idx) => (
            <RuleListItem
              key={rule.id}
              rule={rule}
              traffic={traffic.trafficOf(rule)}
              isFirst={idx === 0}
              isLast={idx === filteredRules.length - 1}
              onConfigure={(id) =>
                navigate(`/features/data-masking/edit/${id}`)
              }
              onConfigureConnection={(name) =>
                navigate(`/resources/configure/${encodeURIComponent(name)}`)
              }
            />
          ))}
        </Box>
      )}
    </Stack>
  )
}
