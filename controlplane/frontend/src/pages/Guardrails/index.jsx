import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { ListVideo, Rotate3d } from 'lucide-react'
import AsyncValueFilter from '@/components/AsyncValueFilter'
import Button from '@/components/Button'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import PageLoader from '@/components/PageLoader'
import ValueFilter from '@/components/ValueFilter'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import EmptyState from '@/layout/EmptyState'
import FullBleed from '@/layout/FullBleed'
import { useUIStore } from '@/stores/useUIStore'
import { useUserStore } from '@/stores/useUserStore'
import { LICENSE_FEATURE_LABELS, licenseRequiredMessage, licenseState } from '@/utils/license'
import { useGuardrailsStore } from './store'
import GuardrailListItem from './components/GuardrailListItem'
import GuardrailsPromotion from './components/GuardrailsPromotion'

export default function Guardrails() {
  const navigate = useNavigate()

  const list = useGuardrailsStore((s) => s.list)
  const listStatus = useGuardrailsStore((s) => s.listStatus)
  const fetchList = useGuardrailsStore((s) => s.fetchList)

  // Creating a rule needs a valid Enterprise license (hard zero on the client).
  // Editing and deleting the rules that exist stay open in every state.
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const licenseInfo = useUserStore((s) => s.licenseInfo)
  const openLicenseModal = useUIStore((s) => s.openLicenseModal)
  // A DLP provider (gcp or mspresidio) is required to enforce guardrails;
  // has_redact_credentials is true only when one of those is configured.
  const hasRedactCredentials = useUserStore((s) => s.hasRedactCredentials)

  const [selectedRole, setSelectedRole] = useState(null)

  const roleFilter = usePaginatedConnections({ pageSize: 50 })

  useEffect(() => {
    fetchList()
  }, [fetchList])

  const filteredGuardrails = useMemo(() => {
    let guardrails = list
    if (selectedRole) {
      guardrails = guardrails.filter((guardrail) =>
        (guardrail.connection_ids ?? []).includes(selectedRole.value),
      )
    }
    return guardrails
  }, [list, selectedRole])

  const createBlocked = isFreeLicense
  const licenseMessage = licenseRequiredMessage(
    LICENSE_FEATURE_LABELS.guardrails,
    licenseState(licenseInfo),
  )
  const loading = listStatus === 'loading'
  const showLoader = useMinDelay(loading && list.length === 0, 500)
  const activeFilterCount = selectedRole ? 1 : 0

  const goCreate = () => navigate('/guardrails/new')

  if (showLoader) {
    return <PageLoader h={300} />
  }

  // A failed load leaves the list empty, which would otherwise fall through to
  // the empty state and tell an admin they have no guardrails configured.
  if (listStatus === 'error') {
    return <PageLoader error h={300} message="Failed to load guardrails." />
  }

  // Without a DLP provider guardrails cannot be enforced, so the requirement
  // screen replaces the list even when guardrails already exist.
  if (!hasRedactCredentials) {
    return (
      <FullBleed>
        <GuardrailsPromotion dlpAvailable={false} onCreate={goCreate} />
      </FullBleed>
    )
  }

  if (list.length === 0) {
    return (
      <FullBleed>
        <GuardrailsPromotion
          dlpAvailable
          onCreate={goCreate}
          onAddLicense={createBlocked ? openLicenseModal : undefined}
        />
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
        <Button onClick={goCreate} disabled={createBlocked}>
          Create a new Guardrail
        </Button>
      </Group>

      {createBlocked && <FreeLicenseCallout message={licenseMessage} />}

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

      {filteredGuardrails.length === 0 ? (
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
