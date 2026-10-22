import { useEffect } from 'react'
import { Link } from 'react-router-dom'
import { Anchor, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import PageLoader from '@/components/PageLoader'
import SectionRow from '@/components/SectionRow'
import Table from '@/components/Table'
import { SidecarStatusBadge } from '@/pages/Sidecars/components/SidecarDetails'
import { analyzerLabel, usesConfigFile } from '@/pages/Sidecars/config'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { useUserStore } from '@/stores/useUserStore'

function Dimmed({ children }) {
  return (
    <Text size="sm" c="dimmed">
      {children}
    </Text>
  )
}

function Model({ sidecar }) {
  const { text, set } = analyzerLabel(sidecar)
  return set ? <Text size="sm">{text}</Text> : <Dimmed>{text}</Dimmed>
}

function Rules({ sidecar }) {
  if (usesConfigFile(sidecar)) return <Dimmed>Not delivered</Dimmed>
  if (sidecar.bound_rules_unavailable) return <Dimmed>Could not be loaded</Dimmed>
  const names = [...new Set((sidecar.bound_rules ?? []).filter((b) => b.kind === 'analyzer').map((b) => b.rule_name))]
  return names.length > 0 ? <Text size="sm">{names.join(', ')}</Text> : <Dimmed>None</Dimmed>
}

export default function SidecarAnalyzers() {
  const sidecars = useSidecarStore((s) => s.sidecars)
  const loading = useSidecarStore((s) => s.loading)
  const error = useSidecarStore((s) => s.error)
  const fetchSidecars = useSidecarStore((s) => s.fetchSidecars)
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const serverInfoLoaded = useUserStore((s) => s.serverInfoLoaded)
  // Every sidecar route answers 403 without an Enterprise license.
  const locked = !serverInfoLoaded || isFreeLicense

  useEffect(() => {
    if (!locked) fetchSidecars()
  }, [locked, fetchSidecars])

  if (locked) return null

  let body
  if (loading && sidecars.length === 0) {
    body = <PageLoader h={120} />
  } else if (error) {
    body = <Text c="red">{error}</Text>
  } else if (sidecars.length === 0) {
    body = (
      <Text size="sm" c="dimmed">
        {'No sidecars yet. '}
        <Anchor component={Link} to="/sidecars" size="sm">
          Add a sidecar
        </Anchor>
      </Text>
    )
  } else {
    body = (
      <Table scrollable>
        <Table.Thead>
          <Table.Tr>
            <Table.Th>Sidecar</Table.Th>
            <Table.Th>Status</Table.Th>
            <Table.Th>Source</Table.Th>
            <Table.Th>Model</Table.Th>
            <Table.Th>Rules</Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {sidecars.map((sidecar) => (
            <Table.Tr key={sidecar.id}>
              <Table.Td>
                <Anchor component={Link} to={`/sidecars/${encodeURIComponent(sidecar.id)}`} size="sm" fw={500}>
                  {sidecar.name}
                </Anchor>
              </Table.Td>
              <Table.Td>
                <SidecarStatusBadge sidecar={sidecar} />
              </Table.Td>
              <Table.Td>
                <Badge variant="light" color={usesConfigFile(sidecar) ? 'gray' : 'blue'} fullLabel>
                  {usesConfigFile(sidecar) ? 'Config file' : 'Hoop'}
                </Badge>
              </Table.Td>
              <Table.Td>
                <Model sidecar={sidecar} />
              </Table.Td>
              <Table.Td>
                <Rules sidecar={sidecar} />
              </Table.Td>
            </Table.Tr>
          ))}
        </Table.Tbody>
      </Table>
    )
  }

  return (
    <SectionRow
      title="Sidecars"
      description="Each sidecar runs the model in its own analyzer section. A rule applies only to a sidecar that has one."
    >
      {body}
    </SectionRow>
  )
}
