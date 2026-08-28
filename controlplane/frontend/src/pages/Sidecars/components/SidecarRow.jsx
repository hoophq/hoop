import { useState } from 'react'
import { Box, Collapse, Flex, Group, Stack, Text, UnstyledButton } from '@mantine/core'
import { ChevronRight, TriangleAlert } from 'lucide-react'
import Badge from '@/components/Badge'
import Table from '@/components/Table'
import Tooltip from '@/components/Tooltip'
import { SIDECAR_STATES } from '../constants'
import { generationLabel, hasObserveOnly, resourceSummary, timeAgo } from '../helpers'
import classes from './SidecarRow.module.css'

/**
 * One sidecar, with its resources underneath.
 *
 * A resource is a listener — one upstream, its own rules. The docs put both on
 * one surface: the Control Plane's job is "which Sidecars are running, what each
 * one resolved, and when it last checked in". The expansion is the middle third.
 *
 * There is no resource detail route, deliberately. A row keyed on
 * (sidecar, listener) would contradict a fleet-wide resource index, which is
 * keyed on `connection` — one connection is served by many sidecars. Keeping the
 * expansion inline leaves that door open.
 */
export default function SidecarRow({ sidecar, isFirst, isLast }) {
  const [open, setOpen] = useState(false)

  const state = SIDECAR_STATES[sidecar.state] ?? SIDECAR_STATES.disconnected
  const lanes = sidecar.lanes ?? []
  const warn = hasObserveOnly(lanes)

  return (
    <Box
      className={classes.row}
      data-first={isFirst || undefined}
      data-last={isLast || undefined}
    >
      <UnstyledButton
        className={classes.header}
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
      >
        <Flex p="lg" align="center" gap="md">
          <ChevronRight
            size={16}
            className={classes.chevron}
            data-open={open || undefined}
            aria-hidden="true"
          />

          <Stack gap={4} flex={1} miw={0}>
            <Text fw={500} fz="lg">
              {sidecar.name}
            </Text>
            <Group gap="xs">
              <Text size="sm" c="dimmed">
                {resourceSummary(lanes)}
              </Text>
              {warn && (
                <Tooltip label="A listener that inspects and audits but denies nothing. Right for a rollout, wrong to leave.">
                  <TriangleAlert size={14} className={classes.warnIcon} />
                </Tooltip>
              )}
            </Group>
          </Stack>

          <Text size="sm" c="dimmed" w={90} ta="right">
            {sidecar.version}
          </Text>
          <Tooltip label="The generation the sidecar reported applying, against the one we issued.">
            <Text size="sm" c="dimmed" w={80} ta="right">
              {generationLabel(sidecar.generation)}
            </Text>
          </Tooltip>
          <Text size="sm" c="dimmed" w={90} ta="right">
            {timeAgo(sidecar.last_seen)}
          </Text>
          <Box w={120} ta="right">
            <Tooltip label={state.hint}>
              <Badge variant={state.variant}>{state.label}</Badge>
            </Tooltip>
          </Box>
        </Flex>
      </UnstyledButton>

      {/* A rejected sidecar kept its previous config. The reason is why, and it
          is the field an operator would otherwise go to the logs for. */}
      {sidecar.reason && (
        <Box className={classes.reason} px="lg" pb="md">
          <Text size="sm">{sidecar.reason}</Text>
        </Box>
      )}

      <Collapse in={open}>
        <Box className={classes.lanes} p="lg">
          <Table striped={false}>
            <Table.Thead>
              <Table.Tr>
                <Table.Th>Resource</Table.Th>
                <Table.Th>Protocol</Table.Th>
                <Table.Th>Upstream</Table.Th>
                <Table.Th>Policy</Table.Th>
                <Table.Th>Masking</Table.Th>
                <Table.Th>Rules</Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {lanes.map((lane) => (
                <Table.Tr key={lane.name}>
                  <Table.Td>
                    <Text size="sm" fw={500}>
                      {lane.connection}
                    </Text>
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
                  <Table.Td>
                    <Text size="sm" c="dimmed">
                      {/* Rule names only. /config withholds the bodies, because a
                          pattern_regex can encode business logic. */}
                      {(lane.rules ?? []).join(', ') || '—'}
                    </Text>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Box>
      </Collapse>
    </Box>
  )
}
