import { useState } from 'react'
import { Group, Image, Stack, Text } from '@mantine/core'
import { ChevronDown, ChevronRight, Plus } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import ActionMenu from '@/components/ActionMenu'
import Button from '@/components/Button'
import Table from '@/components/Table'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { listenerFeatures, protocolInfo } from '../config'
import { listenerLabel } from '../listeners'
import FeaturePills from '../components/FeaturePills'
import ListenerDetails from './ListenerDetails'

function ProtocolCell({ protocol, getIcon }) {
  const info = protocolInfo(protocol)
  return (
    <Group gap={6} wrap="nowrap">
      {/* grpc and spanner are not hoop connection types and have no icon of
          their own; the fallback would show something unrelated. */}
      {info.subtype && <Image src={getIcon({ subtype: info.subtype })} alt="" w={16} h={16} fit="contain" />}
      <Text size="sm">{info.label}</Text>
    </Group>
  )
}

/**
 * The lanes the control plane serves this sidecar, with the controls to author
 * them when a caller passes some.
 *
 * Without `onAdd`/`onEdit`/`onDelete` it renders the same rows read-only. The
 * wizard's Overview step is that caller: it holds its own copy of a sidecar
 * that has not finished connecting, so there is nowhere to put a result.
 * Expanding a row works either way — reading a lane is not authoring it.
 *
 * A listener is addressed by its POSITION in the document. There is no id: the
 * listener is an element of the configuration JSON, not a row, and the name is
 * editable.
 */
export default function ListenersTable({ sidecar, onAdd, onEdit, onDelete }) {
  const getIcon = useConnectionIconGetter()
  const [expanded, setExpanded] = useState(() => new Set())
  const config = sidecar.configuration
  const listeners = config?.listeners ?? []
  const editable = Boolean(onAdd || onEdit || onDelete)

  // Keyed by position, like every other reference to a listener here.
  const toggle = (index) =>
    setExpanded((current) => {
      const next = new Set(current)
      if (next.has(index)) next.delete(index)
      else next.add(index)
      return next
    })

  // The chevron, the five data columns, and the action menu when there is one.
  const columnCount = 6 + (editable ? 1 : 0)

  return (
    <Stack gap="md">
      <Group justify="space-between" align="center">
        <Group gap="sm" align="baseline">
          <Text fw={600}>Listeners</Text>
          <Text size="sm" c="dimmed">
            {`${listeners.length} ${listeners.length === 1 ? 'listener' : 'listeners'}`}
          </Text>
        </Group>
        {editable && (
          <Button size="xs" leftSection={<Plus size={14} />} onClick={onAdd}>
            Add listener
          </Button>
        )}
      </Group>

      {listeners.length === 0 ? (
        <Text size="sm" c="dimmed">
          {editable
            ? 'No listeners yet. A sidecar needs at least one to start.'
            : 'No listeners here. A connected sidecar reads them from its own config file.'}
        </Text>
      ) : (
        <Table>
          <Table.Thead>
            <Table.Tr>
              <Table.Th aria-label="Expand" w={40} />
              <Table.Th>Name</Table.Th>
              <Table.Th>Protocol</Table.Th>
              <Table.Th>Listen</Table.Th>
              <Table.Th>Upstream</Table.Th>
              <Table.Th>Features</Table.Th>
              {editable && <Table.Th aria-label="Actions" w={56} />}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {/* An array of two rows, not a fragment, so both stay direct
                children of Tbody (pages/Settings/AuditLogs does the same). The
                toggle is on the chevron rather than the whole row: the row
                carries a menu, and a click that both opens a menu and collapses
                what is under it reads as a bug. */}
            {listeners.map((listener, index) => {
              const open = expanded.has(index)
              const label = listenerLabel(listener, index)
              return [
                <Table.Tr key={`${label}-${index}`}>
                  <Table.Td>
                    <ActionIcon
                      variant="subtle"
                      color="gray"
                      onClick={() => toggle(index)}
                      aria-expanded={open}
                      aria-label={`${open ? 'Hide' : 'Show'} details for ${label}`}
                    >
                      {open ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                    </ActionIcon>
                  </Table.Td>
                  <Table.Td miw={140}>
                    {/* An unnamed lane shows the name the daemon gives it, so
                        the row matches its own audit rows and log lines. */}
                    <Text size="sm" fw={600} c={listener.name ? undefined : 'dimmed'}>
                      {label}
                    </Text>
                  </Table.Td>
                  <Table.Td miw={140}>
                    <ProtocolCell protocol={listener.protocol} getIcon={getIcon} />
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" ff="monospace">
                      {listener.listen}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" ff="monospace">
                      {listener.upstream}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <FeaturePills features={listenerFeatures(listener, config)} />
                  </Table.Td>
                  {editable && (
                    <Table.Td>
                      <ActionMenu>
                        <ActionMenu.Item onClick={() => onEdit(index)}>Edit</ActionMenu.Item>
                        <ActionMenu.Divider />
                        <ActionMenu.Item danger onClick={() => onDelete(index)}>
                          Delete
                        </ActionMenu.Item>
                      </ActionMenu>
                    </Table.Td>
                  )}
                </Table.Tr>,
                open && (
                  <Table.Tr key={`${label}-${index}-details`}>
                    <Table.Td colSpan={columnCount} p={0}>
                      <ListenerDetails listener={listener} config={config} />
                    </Table.Td>
                  </Table.Tr>
                ),
              ]
            })}
          </Table.Tbody>
        </Table>
      )}
    </Stack>
  )
}
