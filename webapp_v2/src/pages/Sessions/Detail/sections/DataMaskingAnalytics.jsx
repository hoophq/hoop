import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Anchor, Box, Group, SimpleGrid, Stack, Text } from '@mantine/core'
import { ArrowRightLeft, ArrowUpRight, ChevronDown, LayoutList, Sparkles } from 'lucide-react'
import Accordion from '@/components/Accordion'
import { useUserStore } from '@/stores/useUserStore'
import { docsUrl } from '@/utils/docsUrl'
import { useSessionsStore } from '../../store'

/**
 * Port of `audit/views/data_masking_analytics.cljs` — the Live Data Masking
 * block inside the session details modal.
 *
 * Three mutually exclusive states, same order as v1's `cond`:
 *   1. gateway is not running Presidio  → upsell callout, no numbers
 *   2. free license                     → accordion with an upsell subtitle
 *   3. otherwise                        → accordion with the collapsed summary
 *
 * The report is fetched by the caller (v1 subscribes to `:reports->session`).
 * The RDP PII analysis tracker that v1 renders in the subtitle slot is not
 * part of this port.
 */

const ITEM_VALUE = 'live-data-masking-analytics'
const DATA_MASKING_PATH = '/features/data-masking'
const ICON_COLOR = 'var(--mantine-color-violet-6)'

/** v1's `utilities/sanitize-string`: "PHONE_NUMBER" → "Phone Number". */
const MINOR_WORDS = new Set(['of', 'the', 'and'])

function sanitize(infoType) {
  return String(infoType ?? '')
    .split(/[_-]/)
    .map((word) => {
      const lower = word.toLowerCase()
      return MINOR_WORDS.has(lower) ? lower : lower.charAt(0).toUpperCase() + lower.slice(1)
    })
    .join(' ')
}

/**
 * v1's `build-analytics-report`: the report endpoint wins, but only once it is
 * ready and actually carries numbers. Otherwise the session's own
 * `metrics.data_analyzer` map ({ infoType: count }) is aggregated instead.
 */
function resolveReport(report, session) {
  const reportItems = report?.data?.items ?? []
  const reportTotal = report?.data?.total_redact_count ?? 0

  if (report?.status === 'ready' && (reportItems.length > 0 || reportTotal > 0)) {
    return { items: reportItems, total: reportTotal }
  }

  const entries = Object.entries(session?.metrics?.data_analyzer ?? {})
  return {
    items: entries.map(([infoType, count]) => ({ info_type: infoType, count })),
    total: entries.reduce((sum, [, count]) => sum + (count ?? 0), 0),
  }
}

/** Link followed by the "opens elsewhere" arrow, as in every v1 upsell row. */
function ArrowLink({ children, arrowSize = 16, ...props }) {
  return (
    <Anchor underline="never" fw={500} size="sm" {...props}>
      <Group component="span" display="inline-flex" gap={4} wrap="nowrap" align="center">
        {children}
        <ArrowUpRight size={arrowSize} />
      </Group>
    </Anchor>
  )
}

function Tile({ icon, label, value }) {
  const Icon = icon
  return (
    <Stack align="center" justify="center" gap="xs" bg="violet.1" p="xs" bdrs="sm">
      <Icon size={16} color={ICON_COLOR} />
      <Text size="xs">{label}</Text>
      <Text size="sm" fw={600} ta="center">
        {value}
      </Text>
    </Stack>
  )
}

function UpsellCallout({ onNavigate }) {
  return (
    <Box bg="violet.0" p="md" bdrs="md">
      <Group align="flex-start" gap="sm" wrap="nowrap">
        <Sparkles size={16} color={ICON_COLOR} />
        <Stack gap="xs" align="flex-start">
          <Text size="sm" fw={700}>
            Unlock Live Data Masking
          </Text>
          <Text size="sm">
            Redact sensitive fields on the fly to reduce exposure risk and keep your data pipelines
            compliant.
          </Text>
          <ArrowLink component={Link} to={DATA_MASKING_PATH} onClick={onNavigate}>
            Configure it on Live Data Masking
          </ArrowLink>
          <ArrowLink
            href={docsUrl.features.aiDatamasking}
            target="_blank"
            rel="noopener noreferrer"
          >
            Go to Live Data Masking Docs
          </ArrowLink>
        </Stack>
      </Group>
    </Box>
  )
}

function Analytics({ items, total, title = 'Live Data Masking', subtitle, hideSummary }) {
  // Controlled so the collapsed summary can be dropped while the panel is open,
  // like v1's `group-data-[state=open]:hidden`.
  const [openItem, setOpenItem] = useState(null)

  const types = items.map((item) => sanitize(item.info_type))
  const totalItemsText = `${total} ${total <= 1 ? 'item' : 'items'}`
  const categoriesDisplay = types.length > 0 ? `${types.length} (${types.join(', ')})` : '-'
  const categoriesSummary =
    types.length === 0
      ? '0'
      : types.length - 1 >= 1
        ? `${types.length} (${types[0]} + ${types.length - 1} more)`
        : `${types.length} (${types[0]})`

  const showSummary = !hideSummary && openItem !== ITEM_VALUE

  return (
    <Accordion
      variant="filled"
      radius="md"
      value={openItem}
      onChange={setOpenItem}
      chevronSize={16}
      chevron={<ChevronDown size={16} color="var(--mantine-color-dimmed)" />}
    >
      {/* The tinted surface is the item itself: the `filled` variant paints the
          expanded item gray, and a background style prop is the only way to keep
          it violet in both states. */}
      <Accordion.Item value={ITEM_VALUE} bg="violet.0" p="sm">
        <Accordion.Control>
          <Group component="span" justify="space-between" align="center" wrap="nowrap" gap="md">
            <Group component="span" gap="xs" wrap="nowrap">
              <Sparkles size={16} color={ICON_COLOR} />
              <Text component="span" size="sm" fw={700}>
                {title}
              </Text>
            </Group>
            {showSummary && (
              <Group component="span" gap="md" justify="flex-end">
                <Text component="span" size="xs">
                  {`Data Categories: ${categoriesSummary}`}
                </Text>
                <Text component="span" size="xs">
                  {`Volume of Data: ${totalItemsText}`}
                </Text>
              </Group>
            )}
          </Group>
        </Accordion.Control>

        {/* v1 nests the subtitle inside the trigger; it carries a link, so it is
            rendered next to the control instead of inside its <button>. */}
        {subtitle && (
          <Box px="md" pb="xs">
            {subtitle}
          </Box>
        )}

        <Accordion.Panel>
          <SimpleGrid cols={2} spacing="xs">
            <Tile icon={LayoutList} label="Data Categories" value={categoriesDisplay} />
            <Tile icon={ArrowRightLeft} label="Volume of Data" value={totalItemsText} />
          </SimpleGrid>
        </Accordion.Panel>
      </Accordion.Item>
    </Accordion>
  )
}

export default function DataMaskingAnalytics({ session, report }) {
  const redactProvider = useUserStore((s) => s.redactProvider)
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  // v1 dispatches [:close-modal] before navigating to the feature page.
  const closeDetail = useSessionsStore((s) => s.closeDetail)

  const { items, total } = resolveReport(report, session)

  if (redactProvider !== 'mspresidio') {
    return <UpsellCallout onNavigate={closeDetail} />
  }

  if (isFreeLicense) {
    return (
      <Analytics
        items={items}
        total={total}
        hideSummary
        title="Enable Live Data Masking"
        subtitle={
          <Text size="sm">
            {'We detected sensitive data that could protected with automated data masking. '}
            <ArrowLink
              component={Link}
              to={DATA_MASKING_PATH}
              onClick={closeDetail}
              arrowSize={14}
            >
              Configure it on Live Data Masking
            </ArrowLink>
          </Text>
        }
      />
    )
  }

  return <Analytics items={items} total={total} />
}
