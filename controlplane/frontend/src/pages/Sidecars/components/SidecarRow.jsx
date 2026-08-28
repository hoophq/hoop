import { Box, Flex, Group, Stack, Text, UnstyledButton } from '@mantine/core'
import { TriangleAlert } from 'lucide-react'
import Badge from '@/components/Badge'
import Tooltip from '@/components/Tooltip'
import { ROW_HEIGHT, SIDECAR_STATES } from '../constants'
import { generationLabel, hasObserveOnly, resourceSummary, timeAgo } from '../helpers'
import classes from './SidecarRow.module.css'

/**
 * One sidecar. Detail opens in a drawer, not inline.
 *
 * EVERY ROW IS EXACTLY `ROW_HEIGHT`, AND THAT IS THE POINT. This list grows with
 * the fleet, and per-user pods put one sidecar per engineer — a few thousand
 * rows is an ordinary shape, not a worst case. A constant height is what makes
 * windowing a container swap later instead of a rewrite: `index * ROW_HEIGHT`
 * needs no measurement cache, no invalidation on interaction, and no scroll jump
 * when a row above the viewport changes size.
 *
 * The height is set, not inherited from the content, so adding a third line
 * overflows visibly rather than quietly costing the property. Every flexible
 * text truncates for the same reason. A rejected sidecar carries its reason in
 * the badge tooltip and in the drawer, never as another line.
 */
export default function SidecarRow({ sidecar, selected, onSelect, isFirst, isLast }) {
  const state = SIDECAR_STATES[sidecar.state] ?? SIDECAR_STATES.disconnected
  const lanes = sidecar.lanes ?? []
  const warn = hasObserveOnly(lanes)

  return (
    <Box
      className={classes.row}
      data-first={isFirst || undefined}
      data-last={isLast || undefined}
      data-selected={selected || undefined}
    >
      <UnstyledButton
        className={classes.hit}
        onClick={() => onSelect(sidecar)}
        aria-label={`${sidecar.name} — open details`}
      >
        <Flex px="lg" h={ROW_HEIGHT} align="center" gap="md">
          <Stack gap={2} flex={1} miw={0}>
            <Text fw={500} truncate>
              {sidecar.name}
            </Text>
            <Group gap="xs" wrap="nowrap">
              <Text size="sm" c="dimmed" truncate>
                {resourceSummary(lanes)}
              </Text>
              {warn && (
                <Tooltip label="A listener that inspects and audits but denies nothing. Right for a rollout, wrong to leave.">
                  <TriangleAlert size={14} className={classes.warnIcon} />
                </Tooltip>
              )}
            </Group>
          </Stack>

          <Text size="sm" c="dimmed" w={72} ta="right" truncate>
            {sidecar.version}
          </Text>
          <Tooltip label="The generation the sidecar reported applying, against the one we issued.">
            <Text size="sm" c="dimmed" w={72} ta="right" truncate>
              {generationLabel(sidecar.generation)}
            </Text>
          </Tooltip>
          <Text size="sm" c="dimmed" w={80} ta="right" truncate>
            {timeAgo(sidecar.last_seen)}
          </Text>
          <Box w={124} ta="right">
            {/* A rejected sidecar kept its previous config, and the reason is why.
                It rides the badge rather than a third line, because a third line
                only on rejected rows is exactly the uneven height this list
                cannot afford. Full text is in the drawer. */}
            <Tooltip label={sidecar.reason || state.hint} multiline maw={320}>
              <Badge variant={state.variant}>{state.label}</Badge>
            </Tooltip>
          </Box>
        </Flex>
      </UnstyledButton>
    </Box>
  )
}
