import { useNavigate } from 'react-router-dom'
import { Group, Image, Stack, Text } from '@mantine/core'
import { Plus } from 'lucide-react'
import ActionMenu from '@/components/ActionMenu'
import Button from '@/components/Button'
import Table from '@/components/Table'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { listenerFeatures, protocolInfo } from '../config'
import FeaturePills from '../components/FeaturePills'

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
 *
 * A listener is addressed by its POSITION in the document. There is no id: the
 * listener is an element of the configuration JSON, not a row, and the name is
 * editable.
 *
 * Both edit shells are wired while the two are being compared: the row menu
 * opens the modal, and "Edit on full page" opens the route. One of them goes
 * before this ships for good.
 */
export default function ListenersTable({ sidecar, onAdd, onEdit, onDelete }) {
  const editable = Boolean(onAdd || onEdit || onDelete)
  const navigate = useNavigate()
  const getIcon = useConnectionIconGetter()
  const config = sidecar.configuration
  const listeners = config?.listeners ?? []

  const fullPage = (listener) =>
    navigate(`/sidecars/${encodeURIComponent(sidecar.id)}/listeners/${encodeURIComponent(listener.name)}`)

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
              <Table.Th>Name</Table.Th>
              <Table.Th>Protocol</Table.Th>
              <Table.Th>Listen</Table.Th>
              <Table.Th>Upstream</Table.Th>
              <Table.Th>Features</Table.Th>
              {editable && <Table.Th aria-label="Actions" w={56} />}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {listeners.map((listener, index) => (
              <Table.Tr key={`${listener.name}-${index}`}>
                <Table.Td miw={140}>
                  <Text size="sm" fw={600} c={listener.name ? undefined : 'dimmed'}>
                    {listener.name || 'Unnamed'}
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
                      {/* The route keys on the name, and a listener seeded
                          from a file may have none — the daemon defaults it to
                          listener[i] rather than storing one. The modal works
                          on the position and does not care. */}
                      {listener.name && (
                        <ActionMenu.Item onClick={() => fullPage(listener)}>Edit on full page</ActionMenu.Item>
                      )}
                      <ActionMenu.Divider />
                      <ActionMenu.Item danger onClick={() => onDelete(index)}>
                        Delete
                      </ActionMenu.Item>
                    </ActionMenu>
                  </Table.Td>
                )}
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
    </Stack>
  )
}
