import { useEffect, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { Group, Stack, Text, Title } from '@mantine/core'
import ActionMenu from '@/components/ActionMenu'
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
import { guardrailSummary, ruleRows, targetSummary } from '@/pages/sidecarRuleRows'
import { useGuardrailsStore } from './store'
import GuardrailsPromotion from './components/GuardrailsPromotion'

// Guardrails as a fleet of sidecars runs them. The sibling of
// ./GatewayGuardrails; neither imports the other and Router.jsx picks one.
//
// Two things are absent, and both follow from the product. There is no DLP
// requirement screen: a sidecar evaluates its own rules in process and needs
// no provider. There are no Resource Role or Attribute filters: a control
// plane has no connections, and a rule reaches a sidecar by naming listeners.
//
// What IS here that the gateway's list has not got is a row per RULE. A
// guardrail record holds a list of them, and the record is a folder — useful
// to save and edit together, useless to scan. The row an operator is looking
// for is the rule.

const FREE_LICENSE_LIMIT_MESSAGE =
  'Your organization has reached Guardrails free usage limits. Upgrade to Enterprise to keep your sensitive data protected.'

function Targets({ labels }) {
  if (labels.length === 0) {
    // Stored and distributed to nobody is a valid state, and a silent blank
    // cell reads as a rendering gap rather than as the fact it is.
    return (
      <Text size="sm" c="dimmed">
        Not distributed
      </Text>
    )
  }
  return (
    <Group gap="xs">
      {labels.map((label) => (
        <Badge key={label} tag chip variant="light" color="gray">
          {label}
        </Badge>
      ))}
    </Group>
  )
}

export default function ControlPlaneGuardrails() {
  const navigate = useNavigate()

  const list = useGuardrailsStore((s) => s.list)
  const listStatus = useGuardrailsStore((s) => s.listStatus)
  const fetchList = useGuardrailsStore((s) => s.fetchList)

  const sidecars = useSidecarStore((s) => s.sidecars)
  const fetchSidecars = useSidecarStore((s) => s.fetchSidecars)

  const isFreeLicense = useUserStore((s) => s.isFreeLicense)

  useEffect(() => {
    fetchList()
    // The fleet names the listeners a rule is bound to. Without it every row
    // would print a UUID.
    fetchSidecars()
  }, [fetchList, fetchSidecars])

  const sidecarsById = useMemo(() => new Map(sidecars.map((sc) => [sc.id, sc])), [sidecars])
  const rows = useMemo(() => ruleRows(list, guardrailSummary), [list])

  const atFreeLimit = isFreeLicense && list.length >= 1
  const loading = listStatus === 'loading'
  const showLoader = useMinDelay(loading && list.length === 0, 500)

  const goCreate = () => navigate('/guardrails/new')

  if (showLoader) return <PageLoader h={300} />

  // A failed load leaves the list empty, which would otherwise fall through to
  // the empty state and tell an admin they have no guardrails configured.
  if (listStatus === 'error') {
    return <PageLoader error h={300} message="Failed to load guardrails." />
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
            Rules your sidecars evaluate before a statement reaches the resource.
          </Text>
        </Stack>
        <Button onClick={goCreate} disabled={atFreeLimit}>
          Create a new Guardrail
        </Button>
      </Group>

      {atFreeLimit && <FreeLicenseCallout message={FREE_LICENSE_LIMIT_MESSAGE} variant="limit" />}

      {rows.length === 0 ? (
        // Records exist but none carries a rule. Not the promotion screen:
        // an admin who already created a guardrail is not a new user.
        <EmptyState
          compact
          title="No rules yet"
          description="Your guardrails carry no rules. Open one and add the first."
        />
      ) : (
        <Table scrollable>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Rule</Table.Th>
              <Table.Th>Matches</Table.Th>
              <Table.Th>Distributed to</Table.Th>
              <Table.Th>Guardrail</Table.Th>
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
                {/* The folder the rule is saved in, and what Edit opens. It
                    is the last column because it is the least of what an
                    operator came here to read. */}
                <Table.Td miw={160}>
                  <Text size="sm" c="dimmed">
                    {row.record.name}
                  </Text>
                </Table.Td>
                <Table.Td>
                  {/* Hoop-managed guardrails (protection profiles) are
                      immutable — the API rejects updates. */}
                  {!row.record.managed_by && (
                    <ActionMenu>
                      <ActionMenu.Item onClick={() => navigate(`/guardrails/edit/${row.record.id}`)}>
                        Edit
                      </ActionMenu.Item>
                    </ActionMenu>
                  )}
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
    </Stack>
  )
}
