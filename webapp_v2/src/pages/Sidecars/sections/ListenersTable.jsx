import { useState } from 'react'
import { Box, Group, Image, Stack, Text } from '@mantine/core'
import { ArrowRightFromLine, ArrowRightToLine, ChevronDown, ChevronRight, Pencil, Plus, Search } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Table from '@/components/Table'
import TextInput from '@/components/TextInput'
import { useConnectionIconGetter } from '@/utils/connectionIcons'
import { protocolInfo } from '../config'
import { listenerPolicies } from '../features'
import { listenerLabel } from '../listeners'
import FeatureAccordions from '../components/FeatureAccordions'
import FeaturePills from '../components/FeaturePills'

// Figma draws the two addresses as chips with a direction on them rather than
// as bare text. Inbound is where clients arrive, outbound is where the sidecar
// dials your resource.
//
// A Badge, not a Pill: Pill's label is a block box that centres a bare string
// and nothing else, so an icon beside text lands above the chip's centre.
function AddressChip({ value, inbound }) {
  const Icon = inbound ? ArrowRightToLine : ArrowRightFromLine
  return (
    <Badge tag chip variant="light" color="gray" ff="monospace" icon={<Icon size={12} aria-hidden="true" />}>
      {value}
    </Badge>
  )
}

function ProtocolChip({ protocol, getIcon }) {
  const info = protocolInfo(protocol)
  // grpc and spanner are not hoop connection types and have no icon of their
  // own; the fallback would show something unrelated.
  const icon = info.subtype ? (
    <Image src={getIcon({ subtype: info.subtype })} alt="" w={14} h={14} fit="contain" />
  ) : undefined
  return (
    <Badge tag chip variant="light" color="gray" icon={icon}>
      {info.label}
    </Badge>
  )
}

// Chips when the lane carries a rule of its own; "Inherited policy" when it
// only runs the sidecar's defaults, which is how the Figma row tells the two
// apart.
function PoliciesCell({ listener, config, boundRules }) {
  const { features, inheritedOnly } = listenerPolicies(listener, config, boundRules)
  if (features.length > 0) return <FeaturePills compact features={features} />
  return (
    <Text size="sm" c="dimmed">
      {inheritedOnly ? 'Inherited policy' : 'No policies configured'}
    </Text>
  )
}

const matches = (listener, label, query) =>
  [label, listener.protocol, protocolInfo(listener.protocol).label, listener.listen, listener.upstream].some((v) =>
    String(v ?? '')
      .toLowerCase()
      .includes(query),
  )

/**
 * The lanes the control plane serves this sidecar, with the controls to author
 * them when a caller passes some.
 *
 * Without `onAdd`/`onEdit` it renders the same rows read-only. The wizard's
 * Overview step is that caller: it holds its own copy of a sidecar that has not
 * finished connecting, so there is nowhere to put a result. Expanding a row
 * works either way — reading a lane is not authoring it.
 *
 * A listener is addressed by its POSITION in the document. There is no id: the
 * listener is an element of the configuration JSON, not a row, and the name is
 * editable. The search filters what is shown, never what an index points at.
 */
export default function ListenersTable({ sidecar, onAdd, onEdit }) {
  const getIcon = useConnectionIconGetter()
  const [expanded, setExpanded] = useState(() => new Set())
  const [query, setQuery] = useState('')
  const config = sidecar.configuration
  const listeners = config?.listeners ?? []
  const editable = Boolean(onAdd || onEdit)

  // Keyed by position, like every other reference to a listener here.
  const toggle = (index) =>
    setExpanded((current) => {
      const next = new Set(current)
      if (next.has(index)) next.delete(index)
      else next.add(index)
      return next
    })

  const needle = query.trim().toLowerCase()
  const rows = listeners
    .map((listener, index) => ({ listener, index, label: listenerLabel(listener, index) }))
    .filter(({ listener, label }) => needle === '' || matches(listener, label, needle))

  // The chevron, the five data columns, and the edit column when there is one.
  const columnCount = 6 + (editable ? 1 : 0)

  return (
    <Stack gap="md">
      <Group justify="space-between" align="center">
        <Group gap="md" align="center">
          <Group gap="sm" align="baseline">
            <Text fw={600}>Listeners</Text>
            <Text size="sm" c="dimmed">
              {`${listeners.length} ${listeners.length === 1 ? 'listener' : 'listeners'}`}
            </Text>
          </Group>
          {listeners.length > 0 && (
            <TextInput
              placeholder="Search listeners"
              aria-label="Search listeners"
              leftSection={<Search size={16} />}
              value={query}
              onChange={(e) => setQuery(e.currentTarget.value)}
              w={240}
            />
          )}
        </Group>
        {editable && (
          <Button variant="light" leftSection={<Plus size={16} />} onClick={onAdd}>
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
      ) : rows.length === 0 ? (
        <Text size="sm" c="dimmed">
          {`No listener matches "${query.trim()}".`}
        </Text>
      ) : (
        <Table scrollable>
          <Table.Thead>
            <Table.Tr>
              <Table.Th aria-label="Expand" w={40} />
              <Table.Th>Name</Table.Th>
              <Table.Th>Protocol</Table.Th>
              <Table.Th>Listen</Table.Th>
              <Table.Th>Upstream</Table.Th>
              <Table.Th>Policies</Table.Th>
              {editable && <Table.Th aria-label="Actions" w={96} />}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {/* An array of two rows, not a fragment, so both stay direct
                children of Tbody (pages/Settings/AuditLogs does the same). */}
            {rows.map(({ listener, index, label }) => {
              const open = expanded.has(index)
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
                    <ProtocolChip protocol={listener.protocol} getIcon={getIcon} />
                  </Table.Td>
                  {/* A floor, not a cap: past it the whole table scrolls. */}
                  <Table.Td miw={170}>
                    <AddressChip value={listener.listen} inbound />
                  </Table.Td>
                  <Table.Td miw={200}>
                    <AddressChip value={listener.upstream} />
                  </Table.Td>
                  <Table.Td miw={150}>
                    <PoliciesCell listener={listener} config={config} boundRules={sidecar.bound_rules} />
                  </Table.Td>
                  {editable && (
                    <Table.Td>
                      <Button
                        variant="subtle"
                        size="compact-sm"
                        leftSection={<Pencil size={14} />}
                        onClick={() => onEdit(index)}
                      >
                        Edit
                      </Button>
                    </Table.Td>
                  )}
                </Table.Tr>,
                open && (
                  <Table.Tr key={`${label}-${index}-details`}>
                    <Table.Td colSpan={columnCount} p={0}>
                      <Box p="md" bg="gray.0">
                        <FeatureAccordions listener={listener} config={config} boundRules={sidecar.bound_rules} />
                      </Box>
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
