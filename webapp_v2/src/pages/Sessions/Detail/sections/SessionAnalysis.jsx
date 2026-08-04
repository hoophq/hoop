import { useState } from 'react'
import { Box, Divider, Group, Image, Stack, Text } from '@mantine/core'
import InfoAccordion from '../../components/InfoAccordion'
import Badge from '@/components/Badge'

/**
 * Port of `features/ai_session_analyzer/views/session_analysis.cljs` — the
 * collapsible AI Session Analyzer card shown above the session content.
 *
 * v1 is a Radix single/collapsible Accordion styled as a subtle card
 * (`p-3 bg-[--gray-1] rounded-md border border-[--gray-3]`). Mantine's
 * `contained` variant already draws the border + radius and paints the item
 * `gray.0` while open; `bg`/`bdrs` on the root carry the same surface into the
 * collapsed state so the card never changes color when toggled.
 *
 * The accordion is controlled so the header can hide the title + risk summary
 * while expanded — v1 does this with `group-data-[state=open]:hidden`.
 */

const ITEM_VALUE = 'ai-session-analysis'

const RISK_BADGE_COLOR = {
  low: 'green',
  // v1 says "orange", but the theme only defines indigo/gray/green/amber/red/sky
  // — `color="orange"` would silently fall back to Mantine's stock palette.
  medium: 'amber',
  high: 'red',
}

const ACTION_BADGE = {
  block_execution: { color: 'red', label: 'ACTION BLOCKED' },
  allow_execution: { color: 'green', label: 'ACTION ALLOWED' },
}

function RiskBadge({ riskLevel }) {
  if (!riskLevel) return null

  return (
    <Badge color={RISK_BADGE_COLOR[riskLevel] ?? 'gray'} variant="light">
      {`${riskLevel.toUpperCase()} RISK`}
    </Badge>
  )
}

export default function SessionAnalysis({ aiAnalysis }) {
  const [openedItem, setOpenedItem] = useState(null)

  if (!aiAnalysis || Object.keys(aiAnalysis).length === 0) return null

  const { risk_level: riskLevel, title, explanation, action } = aiAnalysis
  const actionBadge = ACTION_BADGE[action]
  const isOpen = openedItem === ITEM_VALUE

  return (
    <InfoAccordion
      value={openedItem}
      onChange={setOpenedItem}
    >
      <InfoAccordion.Item value={ITEM_VALUE}>
        <InfoAccordion.Control>
          <Group justify="space-between" wrap="nowrap" gap="md">
            <Group gap="xs" wrap="nowrap">
              <Image
                src="/images/ai-session-analyzer-logo.svg"
                alt="AI Session Analyzer"
                w={20}
                h={20}
                miw={20}
                fit="contain"
              />
              <Text size="sm" fw={700}>
                AI Session Analyzer
              </Text>
            </Group>

            {/* v1 collapses the summary away once the panel opens. */}
            {!isOpen && (
              <Group gap="xs" wrap="nowrap">
                <Text size="sm" fw={500}>
                  {title}
                </Text>
                <RiskBadge riskLevel={riskLevel} />
              </Group>
            )}
          </Group>
        </InfoAccordion.Control>

        <InfoAccordion.Panel>
          <Stack gap="md">
            <Group gap="md" wrap="nowrap" align="center">
              <Text size="sm" fw={500}>
                {title}
              </Text>
              <RiskBadge riskLevel={riskLevel} />
            </Group>

            <Text size="xs">{explanation}</Text>

            {/*
              The separator hangs off `action`, the badge off `actionBadge`.
              v1 nests its `case` INSIDE the bordered Box, so an unrecognized
              (but truthy) action still renders the rule and its spacing, just
              without a badge. Gating both on the lookup would swallow that.
            */}
            {action && (
              <>
                <Divider />
                <Box>
                  {actionBadge && (
                    <Badge color={actionBadge.color} variant="light">
                      {actionBadge.label}
                    </Badge>
                  )}
                </Box>
              </>
            )}
          </Stack>
        </InfoAccordion.Panel>
      </InfoAccordion.Item>
    </InfoAccordion>
  )
}
