import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Group, Stack } from '@mantine/core'
import { Rotate3d } from 'lucide-react'
import AsyncValueFilter from '@/components/AsyncValueFilter'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import EmptyState from '@/layout/EmptyState'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { docsUrl } from '@/utils/docsUrl'
import { useAiSessionAnalyzerStore } from './store'
import RuleListItem from './components/RuleListItem'

export default function RulesTab({ providerConfigured, onGoConfigure }) {
  const navigate = useNavigate()

  const list = useAiSessionAnalyzerStore((s) => s.list)

  const [selectedRole, setSelectedRole] = useState(null)
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

  const filteredRules = useMemo(() => {
    if (!selectedRole) return list
    return list.filter((rule) => (rule.connection_names ?? []).includes(selectedRole.value))
  }, [list, selectedRole])

  const goCreate = () => navigate('/features/ai-session-analyzer/rules/new')

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
      </Group>

      {filteredRules.length === 0 ? (
        <EmptyState
          compact
          title="No AI Session Analyzer rules match your filters"
          description="Try clearing the filter above."
        />
      ) : (
        <Box>
          {filteredRules.map((rule, idx) => (
            <RuleListItem
              key={rule.id ?? rule.name}
              rule={rule}
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
