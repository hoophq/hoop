import { Group, Stack, Text } from '@mantine/core'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import Table from '@/components/Table'
import { SIDECAR_STATES } from '../constants'
import { generationLabel, timeAgo } from '../helpers'

function Field({ label, children }) {
  return (
    <Stack gap={2} miw={110}>
      <Text size="xs" c="dimmed" tt="uppercase" fw={600}>
        {label}
      </Text>
      {children}
    </Stack>
  )
}

/**
 * The body of the sidecar drawer: what this one is, and what it resolved.
 *
 * This is where everything that would have made a list row taller lives — the
 * NACK reason in full, the upstream of each listener, the rule names. The list
 * keeps uniform rows; the detail is unbounded here, where nothing has to be
 * measured.
 */
export default function SidecarDetail({ sidecar }) {
  if (!sidecar) return null

  const state = SIDECAR_STATES[sidecar.state] ?? SIDECAR_STATES.disconnected
  const lanes = sidecar.lanes ?? []

  return (
    <Stack gap="xl">
      <Group gap="xl" align="flex-start">
        <Field label="State">
          <Badge variant={state.variant}>{state.label}</Badge>
        </Field>
        <Field label="Version">
          <Text size="sm">{sidecar.version}</Text>
        </Field>
        <Field label="Generation">
          <Text size="sm">{generationLabel(sidecar.generation)}</Text>
        </Field>
        <Field label="Last check-in">
          <Text size="sm">{timeAgo(sidecar.last_seen)}</Text>
        </Field>
      </Group>

      <Text size="sm" c="dimmed">
        {state.hint}
      </Text>

      {/* The reason a sidecar refused a config, in full. It is the field an
          operator would otherwise go to the logs for. */}
      {sidecar.reason && (
        <Alert color="red" title="The sidecar refused this config">
          <Stack gap="xs">
            <Text size="sm">{sidecar.reason}</Text>
            <Text size="sm" c="dimmed">
              {'It is still serving the configuration it had before.'}
            </Text>
          </Stack>
        </Alert>
      )}

      <Stack gap="sm">
        <Text fw={600}>Resources</Text>
        <Text size="sm" c="dimmed">
          {'One listener is one upstream. These are the lanes this sidecar resolved at startup, after inheritance — not what the config file asked for.'}
        </Text>
        <Table striped={false}>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Resource</Table.Th>
              <Table.Th>Protocol</Table.Th>
              <Table.Th>Upstream</Table.Th>
              <Table.Th>Policy</Table.Th>
              <Table.Th>Masking</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {lanes.map((lane) => (
              <Table.Tr key={lane.name}>
                <Table.Td>
                  <Stack gap={2}>
                    <Text size="sm" fw={500}>
                      {lane.connection}
                    </Text>
                    {/* Rule names only. /config withholds the bodies, because a
                        pattern_regex can encode business logic. */}
                    <Text size="xs" c="dimmed">
                      {(lane.rules ?? []).join(', ') || 'no rules'}
                    </Text>
                  </Stack>
                </Table.Td>
                <Table.Td>
                  <Text size="sm" c="dimmed">
                    {lane.protocol}
                  </Text>
                </Table.Td>
                <Table.Td>
                  <Text size="sm" c="dimmed">
                    {lane.upstream}
                  </Text>
                </Table.Td>
                <Table.Td>
                  <Badge variant={lane.enforcing ? 'active' : 'warning'}>
                    {lane.enforcing ? 'Enforcing' : 'Observe-only'}
                  </Badge>
                </Table.Td>
                <Table.Td>
                  <Text size="sm" c="dimmed">
                    {lane.masking ? 'On' : 'Off'}
                  </Text>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Stack>
    </Stack>
  )
}
