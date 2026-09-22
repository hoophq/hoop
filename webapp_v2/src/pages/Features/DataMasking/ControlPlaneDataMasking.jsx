import { useEffect, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { Group, Stack, Text, Title } from '@mantine/core'
import { Info } from 'lucide-react'
import ActionMenu from '@/components/ActionMenu'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import PageLoader from '@/components/PageLoader'
import Table from '@/components/Table'
import { useMinDelay } from '@/hooks/useMinDelay'
import EmptyState from '@/layout/EmptyState'
import FullBleed from '@/layout/FullBleed'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { useUserStore } from '@/stores/useUserStore'
import { maskSummary, ruleRows, targetSummary } from '@/pages/sidecarRuleRows'
import { useDataMaskingStore } from './store'
import DataMaskingPromotion from './components/DataMaskingPromotion'

// Masking as a SIDECAR runs it: the response frame is decoded in process and
// values are rewritten, with no DLP provider and no resource roles. The
// sibling of ./GatewayDataMasking; Router.jsx picks one.
//
// One row per RULE, not per record. A masking record is a folder of rules, and
// the rule is what an operator names and goes looking for.

const FREE_LICENSE_LIMIT_MESSAGE =
  'Your organization has reached Live Data Masking free usage limits. Upgrade to Enterprise to keep your sensitive data protected.'

// Masking does not add up: composition writes a bound listener's whole `mask`
// block, so these rules replace whatever its config file carries. Said once at
// the top of the list, because it is a property of the feature and not of any
// one rule.
const REPLACEMENT_NOTE =
  "On every listener a rule names, these rules become the whole mask block: mask rules in the sidecar's config file stop being applied. Guardrails add up; masking replaces."

function Targets({ labels }) {
  if (labels.length === 0) {
    return (
      <Text size="sm" c="dimmed">
        Not distributed
      </Text>
    )
  }
  return (
    <Group gap="xs">
      {/* The position, not the label: two deleted sidecars both render as
          "Unknown sidecar", so a label is not unique and React would drop the
          second chip — hiding a target. The list is derived fresh from the
          row's own targets and is never reordered, so the index is stable. */}
      {labels.map((label, i) => (
        <Badge key={`${label}#${i}`} tag chip variant="light" color="gray">
          {label}
        </Badge>
      ))}
    </Group>
  )
}

export default function ControlPlaneDataMasking() {
  const navigate = useNavigate()

  const list = useDataMaskingStore((s) => s.list)
  const listStatus = useDataMaskingStore((s) => s.listStatus)
  const fetchList = useDataMaskingStore((s) => s.fetchList)

  const sidecars = useSidecarStore((s) => s.sidecars)
  const fetchSidecars = useSidecarStore((s) => s.fetchSidecars)

  const isFreeLicense = useUserStore((s) => s.isFreeLicense)

  useEffect(() => {
    fetchList()
    fetchSidecars()
  }, [fetchList, fetchSidecars])

  const sidecarsById = useMemo(() => new Map(sidecars.map((sc) => [sc.id, sc])), [sidecars])
  const rows = useMemo(() => ruleRows(list, maskSummary), [list])

  const atFreeLimit = isFreeLicense && list.length >= 1
  const loading = listStatus === 'loading'
  const showLoader = useMinDelay(loading && list.length === 0, 500)

  const goCreate = () => navigate('/features/data-masking/new')

  if (showLoader) return <PageLoader h={300} />

  if (listStatus === 'error') {
    return <PageLoader error h={300} message="Failed to load Live Data Masking rules." />
  }

  if (list.length === 0) {
    return (
      <FullBleed>
        {/* A sidecar carries its own detector (sidecar/pii/alcatraz) and calls
            no provider, so the empty state is always the one that offers the
            create flow rather than a provider requirement. */}
        <DataMaskingPromotion redactProvider="alcatraz" onConfigure={goCreate} />
      </FullBleed>
    )
  }

  return (
    <Stack gap="xl">
      <Group justify="space-between" align="flex-start">
        <Stack gap="sm">
          <Title order={1}>Live Data Masking</Title>
          <Text size="md" c="dimmed">
            Rewrite sensitive values in a response before it leaves the sidecar.
          </Text>
        </Stack>
        <Button onClick={goCreate} disabled={atFreeLimit}>
          Create new
        </Button>
      </Group>

      {atFreeLimit && <FreeLicenseCallout message={FREE_LICENSE_LIMIT_MESSAGE} variant="limit" />}

      <Alert color="blue" variant="light" icon={<Info size={16} />} radius="md">
        {REPLACEMENT_NOTE}
      </Alert>

      {rows.length === 0 ? (
        <EmptyState
          compact
          title="No rules yet"
          description="Your masking records carry no rules. Open one and add the first."
        />
      ) : (
        <Table scrollable>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Rule</Table.Th>
              <Table.Th>Masks</Table.Th>
              <Table.Th>Distributed to</Table.Th>
              <Table.Th>Record</Table.Th>
              <Table.Th aria-label="Actions" w={56} />
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {rows.map((row) => (
              <Table.Tr key={row.key}>
                <Table.Td miw={180}>
                  <Text size="sm" fw={600} c={row.unnamed ? 'dimmed' : undefined}>
                    {row.name}
                  </Text>
                </Table.Td>
                <Table.Td miw={200}>
                  <Text size="sm" c="dimmed">
                    {row.summary}
                  </Text>
                </Table.Td>
                <Table.Td miw={220}>
                  <Targets labels={targetSummary(row.targets, sidecarsById)} />
                </Table.Td>
                <Table.Td miw={160}>
                  <Text size="sm" c="dimmed">
                    {row.record.name}
                  </Text>
                </Table.Td>
                <Table.Td>
                  <ActionMenu>
                    <ActionMenu.Item
                      onClick={() => navigate(`/features/data-masking/edit/${row.record.id}`)}
                    >
                      Edit
                    </ActionMenu.Item>
                  </ActionMenu>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
    </Stack>
  )
}
