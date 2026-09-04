import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { ListVideo, Rotate3d } from 'lucide-react'
import { useUserStore } from '@/stores/useUserStore'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import EmptyState from '@/layout/EmptyState'
import FullBleed from '@/layout/FullBleed'
import PageLoader from '@/components/PageLoader'
import Button from '@/components/Button'
import ValueFilter from '@/components/ValueFilter'
import AsyncValueFilter from '@/components/AsyncValueFilter'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import { useDataMaskingStore } from './store'
import RuleListItem from './components/RuleListItem'
import DataMaskingPromotion from './components/DataMaskingPromotion'

const FREE_LICENSE_LIMIT_MESSAGE =
  'Your organization has reached Live Data Masking free usage limits. Upgrade to Enterprise to keep your sensitive data protected.'

export default function DataMasking() {
  const navigate = useNavigate()

  const list = useDataMaskingStore((s) => s.list)
  const listStatus = useDataMaskingStore((s) => s.listStatus)
  const fetchList = useDataMaskingStore((s) => s.fetchList)

  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const redactProvider = useUserStore((s) => s.redactProvider)

  const [selectedRole, setSelectedRole] = useState(null)

  const roleFilter = usePaginatedConnections({ pageSize: 50 })

  useEffect(() => {
    fetchList()
  }, [fetchList])

  const filteredRules = useMemo(() => {
    let rules = list
    if (selectedRole) {
      rules = rules.filter((rule) =>
        (rule.connection_ids ?? []).includes(selectedRole.value),
      )
    }
    return rules
  }, [list, selectedRole])

  const atFreeLimit = isFreeLicense && list.length >= 1
  const loading = listStatus === 'loading'
  const showLoader = useMinDelay(loading && list.length === 0, 500)
  const activeFilterCount = selectedRole ? 1 : 0

  const goCreate = () => navigate('/features/data-masking/new')

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

  if (list.length === 0) {
    return (
      <FullBleed>
        <DataMaskingPromotion
          redactProvider={redactProvider}
          onConfigure={goCreate}
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
        <Button onClick={goCreate} disabled={atFreeLimit}>
          Create new
        </Button>
      </Group>

      {atFreeLimit && (
        <FreeLicenseCallout message={FREE_LICENSE_LIMIT_MESSAGE} variant="limit" />
      )}

      <Group gap="sm">
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
      </Group>

      {filteredRules.length === 0 ? (
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
              isFirst={idx === 0}
              isLast={idx === filteredRules.length - 1}
              onConfigure={(id) =>
                navigate(`/features/data-masking/edit/${id}`)
              }
            />
          ))}
        </Box>
      )}
    </Stack>
  )
}
