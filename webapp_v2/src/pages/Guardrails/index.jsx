import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Box, Card, Group, Stack, Text, Title } from '@mantine/core'
import { ListVideo, Rotate3d } from 'lucide-react'
import AsyncValueFilter from '@/components/AsyncValueFilter'
import Button from '@/components/Button'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import PageLoader from '@/components/PageLoader'
import ValueFilter from '@/components/ValueFilter'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usePaginatedConnections } from '@/hooks/usePaginatedConnections'
import FullBleed from '@/layout/FullBleed'
import { useUserStore } from '@/stores/useUserStore'
import { useGuardrailsStore } from './store'
import GuardrailListItem from './components/GuardrailListItem'
import GuardrailsPromotion from './components/GuardrailsPromotion'

const FREE_LICENSE_LIMIT_MESSAGE =
  'Your organization has reached Guardrails free usage limits. Upgrade to Enterprise to keep your sensitive data protected.'

function uniqueSorted(values) {
  return [...new Set(values)].sort((a, b) => a.localeCompare(b))
}

export default function Guardrails() {
  const navigate = useNavigate()

  const list = useGuardrailsStore((s) => s.list)
  const listStatus = useGuardrailsStore((s) => s.listStatus)
  const attributes = useGuardrailsStore((s) => s.attributes)
  const fetchList = useGuardrailsStore((s) => s.fetchList)
  const fetchAttributes = useGuardrailsStore((s) => s.fetchAttributes)

  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  // A DLP provider (gcp or mspresidio) is required to enforce guardrails;
  // has_redact_credentials is true only when one of those is configured.
  const hasRedactCredentials = useUserStore((s) => s.hasRedactCredentials)

  const [selectedRole, setSelectedRole] = useState(null)
  const [selectedAttribute, setSelectedAttribute] = useState(null)

  const roleFilter = usePaginatedConnections({ pageSize: 50 })

  useEffect(() => {
    fetchList()
    fetchAttributes()
  }, [fetchList, fetchAttributes])

  const attributeFilterValues = useMemo(
    () => uniqueSorted(attributes.map((a) => a.name)),
    [attributes],
  )

  const filteredGuardrails = useMemo(() => {
    let guardrails = list
    if (selectedRole) {
      guardrails = guardrails.filter((guardrail) =>
        (guardrail.connection_ids ?? []).includes(selectedRole.value),
      )
    }
    if (selectedAttribute) {
      guardrails = guardrails.filter((guardrail) =>
        (guardrail.attributes ?? []).includes(selectedAttribute),
      )
    }
    return guardrails
  }, [list, selectedRole, selectedAttribute])

  const atFreeLimit = isFreeLicense && list.length >= 1
  const loading = listStatus === 'loading'
  const showLoader = useMinDelay(loading && list.length === 0, 500)
  const activeFilterCount = (selectedRole ? 1 : 0) + (selectedAttribute ? 1 : 0)

  const goCreate = () => navigate('/guardrails/new')

  if (showLoader) {
    return <PageLoader h={300} />
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
        <GuardrailsPromotion dlpAvailable onCreate={goCreate} />
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
        <Button onClick={goCreate} disabled={atFreeLimit}>
          Create a new Guardrail
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
        <ValueFilter
          icon={ListVideo}
          label="Attribute"
          values={attributeFilterValues}
          selected={selectedAttribute}
          onSelect={setSelectedAttribute}
          onClear={() => setSelectedAttribute(null)}
        />
      </Group>

      {filteredGuardrails.length === 0 ? (
        <Card padding="xl" withBorder>
          <Stack gap={4} align="center" ta="center">
            <Text fw={600}>No Guardrails match your filters</Text>
            <Text size="sm" c="dimmed">
              {`Try clearing the ${activeFilterCount > 1 ? 'filters' : 'filter'} above.`}
            </Text>
          </Stack>
        </Card>
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
