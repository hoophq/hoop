import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Group, Stack } from '@mantine/core'
import { Network, Rotate3d } from 'lucide-react'
import AsyncValueFilter from '@/components/AsyncValueFilter'
import SidecarListenerFilter from '@/components/SidecarListenerFilter'
import ValueFilter from '@/components/ValueFilter'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import { useRuleTrafficFilter } from '@/hooks/useRuleTrafficFilter'
import EmptyState from '@/layout/EmptyState'
import { boundRuleNames } from '@/pages/Sidecars/config'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { docsUrl } from '@/utils/docsUrl'
import { RULE_KIND_ANALYZER, newRulePath } from '@/utils/ruleTraffic'
import { useAiSessionAnalyzerStore } from './store'
import RuleListItem from './components/RuleListItem'

// A sidecar rule reaches listeners, never a resource role, so each traffic has
// its own filter.
export default function RulesTab({ providerConfigured, onGoConfigure }) {
  const navigate = useNavigate()

  const list = useAiSessionAnalyzerStore((s) => s.list)
  const sidecars = useSidecarStore((s) => s.sidecars)

  const traffic = useRuleTrafficFilter(RULE_KIND_ANALYZER)
  const { traffics, mixed, showAgentFilters, showSidecarFilter, matches } = traffic

  const [selectedRole, setSelectedRole] = useState(null)
  const [selectedTarget, setSelectedTarget] = useState(null)
  const roleFilter = usePaginatedConnections({ pageSize: 50 })
  const getIconUrl = useConnectionIconGetter()

  // Rules reference resource roles by name, so the filter is keyed by name too
  // and its value can be compared with `connection_names` directly.
  const roleOptions = useMemo(
    () =>
      roleFilter.items.map((connection) => ({
        value: connection.name,
        label: connection.name,
        iconUrl: getIconUrl(connection),
      })),
    [roleFilter.items, getIconUrl],
  )

  const target = showSidecarFilter ? selectedTarget : null
  const role = showAgentFilters ? selectedRole : null

  const filteredRules = useMemo(() => {
    let rules = list.filter(matches)
    if (target) {
      const names = boundRuleNames(sidecars, target, 'analyzer')
      rules = rules.filter((rule) => names.has(rule.name))
    }
    if (role) {
      rules = rules.filter((rule) => (rule.connection_names ?? []).includes(role.value))
    }
    return rules
  }, [list, matches, sidecars, target, role])

  const activeFilterCount = (role ? 1 : 0) + (target ? 1 : 0) + (traffic.active ? 1 : 0)

  const goCreate = () =>
    navigate(newRulePath('/features/ai-session-analyzer/rules/new', traffics, traffics[0]))

  // The header's create menu covers both traffics.
  if (list.length === 0 && mixed) {
    return (
      <EmptyState
        title="No rules in your organization yet"
        description="Create one for agent resources or for sidecar listeners."
        docsUrl={docsUrl.features.aiSessionAnalyzer}
        docsLabel="AI Session Analyzer Configuration"
      />
    )
  }

  // Without a provider there is no model to grade sessions with, so the empty
  // state sends the admin to the Configure tab instead of the create form.
  if (list.length === 0) {
    return (
      <EmptyState
        title={
          providerConfigured
            ? 'No rules in your organization yet'
            : 'No configurations in your organization yet'
        }
        action={
          providerConfigured
            ? { label: 'Create new rule', onClick: goCreate }
            : { label: 'Configure AI Session Analyzer', onClick: onGoConfigure }
        }
        docsUrl={docsUrl.features.aiSessionAnalyzer}
        docsLabel="AI Session Analyzer Configuration"
      />
    )
  }

  return (
    <Stack gap="lg">
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
          <AsyncValueFilter
            icon={Rotate3d}
            label="Resource Role"
            placeholder="Search resource roles"
            selected={selectedRole}
            onSelect={setSelectedRole}
            onClear={() => setSelectedRole(null)}
            options={roleOptions}
            loading={roleFilter.loading}
            hasMore={roleFilter.hasMore}
            onLoadMore={roleFilter.loadMore}
            searchValue={roleFilter.searchValue}
            onSearchChange={roleFilter.setSearch}
            onOpen={roleFilter.ensureLoaded}
          />
        )}
      </Group>

      {filteredRules.length === 0 ? (
        <EmptyState
          compact
          title="No AI Session Analyzer rules match your filters"
          description={`Try clearing the ${activeFilterCount > 1 ? 'filters' : 'filter'} above.`}
        />
      ) : (
        <Box>
          {filteredRules.map((rule, idx) => (
            <RuleListItem
              key={rule.id ?? rule.name}
              rule={rule}
              traffic={traffic.trafficOf(rule)}
              isFirst={idx === 0}
              isLast={idx === filteredRules.length - 1}
              onConfigure={(name) =>
                navigate(
                  `/features/ai-session-analyzer/rules/edit/${encodeURIComponent(name)}`,
                )
              }
            />
          ))}
        </Box>
      )}
    </Stack>
  )
}
