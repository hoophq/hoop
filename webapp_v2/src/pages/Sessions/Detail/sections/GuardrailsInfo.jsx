import { Fragment, useState } from 'react'
import { Divider, Group, Stack, Text, ThemeIcon } from '@mantine/core'
import { ShieldCheck } from 'lucide-react'
import InfoAccordion from '../../components/InfoAccordion'
import Badge from '@/components/Badge'
import Code from '@/components/Code'

/**
 * Port of `audit/views/guardrails_info.cljs` — the guardrails block of the
 * session details modal.
 *
 * v1 renders a single collapsible card in both shapes: with exactly one entry
 * the trigger carries the rule name + type badge, with more than one it carries
 * a "Show details"/"Hide details" toggle plus the entry count.
 */

const ITEM_VALUE = 'guardrails'

const RULE_TYPE_CONFIG = {
  pattern_match: { label: 'PATTERN MATCH', color: 'pink' },
  deny_words_list: { label: 'DENY WORD', color: 'red' },
}

function ruleTypeConfig(rule) {
  return (
    RULE_TYPE_CONFIG[rule?.type] ?? {
      label: (rule?.type ?? 'unknown').toUpperCase(),
      color: 'gray',
    }
  )
}

/** v1's `direction-label`: `(str (capitalize (or direction "input")) " Rule:")`. */
function directionLabel(direction) {
  const value = direction || 'input'
  return `${value.charAt(0).toUpperCase()}${value.slice(1).toLowerCase()} Rule:`
}

/** v1's `blocked-message`: only "output" reads as a response, everything else as a query. */
function blockedMessage(ruleType, direction, matchedWords) {
  const subject = direction === 'output' ? 'Response' : 'Query'
  const total = matchedWords?.length ?? 0

  if (ruleType === 'deny_words_list') {
    return `${subject} blocked — ${total} forbidden ${total === 1 ? 'keyword' : 'keywords'} detected`
  }
  if (ruleType === 'pattern_match') {
    return `${subject} blocked — pattern violation detected`
  }
  return `${subject} blocked`
}

function RuleTypeBadge({ rule }) {
  const { label, color } = ruleTypeConfig(rule)
  return (
    <Badge color={color} variant="light">
      {label}
    </Badge>
  )
}

function MatchedWords({ entry }) {
  const words = entry.matched_words ?? []
  if (words.length === 0) return null

  const chips = (
    <Group gap="xs" wrap="wrap">
      {words.map((word, index) => (
        <Code key={`${word}-${index}`} fz="xs" p="xs">
          {word}
        </Code>
      ))}
    </Group>
  )

  // v1 labels the chips only for pattern matches; deny words stand on their own.
  if (entry.rule?.type !== 'pattern_match') return chips

  return (
    <Stack gap="xs">
      <Text size="sm" fw={500}>
        Violation:
      </Text>
      {chips}
    </Stack>
  )
}

/** Panel body of the single-entry card — the rule name lives in the trigger there. */
function EntryDetails({ entry }) {
  return (
    <Stack gap="md">
      <Text size="sm" fw={700}>
        {directionLabel(entry.direction)}
      </Text>
      <Text size="sm">
        {blockedMessage(entry.rule?.type, entry.direction, entry.matched_words)}
      </Text>
      <MatchedWords entry={entry} />
    </Stack>
  )
}

/** Panel body of one entry inside the multi-entry card. */
function MultiEntry({ entry }) {
  return (
    <Stack gap="md">
      <Text size="sm" fw={700}>
        {directionLabel(entry.direction)}
      </Text>
      <Group gap="sm" wrap="nowrap">
        <Text size="sm" fw={500}>
          {entry.rule_name}
        </Text>
        <RuleTypeBadge rule={entry.rule} />
      </Group>
      <Text size="sm">
        {blockedMessage(entry.rule?.type, entry.direction, entry.matched_words)}
      </Text>
      <MatchedWords entry={entry} />
    </Stack>
  )
}

export default function GuardrailsInfo({ guardrailsInfo }) {
  // Controlled so the multi-entry trigger can flip its own label, the way v1
  // does with `group-data-[state=open]` classes.
  const [openedValue, setOpenedValue] = useState(null)

  const entries = guardrailsInfo ?? []
  if (entries.length === 0) return null

  const opened = openedValue === ITEM_VALUE
  const single = entries.length === 1 ? entries[0] : null

  return (
    <InfoAccordion value={openedValue} onChange={setOpenedValue}>
      <InfoAccordion.Item value={ITEM_VALUE}>
        <InfoAccordion.Control
          icon={
            <ThemeIcon color="blue" size={24} radius="sm">
              <ShieldCheck size={16} />
            </ThemeIcon>
          }
        >
          <Group justify="space-between" gap="md" wrap="nowrap">
            <Text size="sm" fw={700}>
              Guardrails
            </Text>
            {single ? (
              <Group gap="sm" wrap="nowrap">
                <Text size="sm" fw={500}>
                  {single.rule_name}
                </Text>
                <RuleTypeBadge rule={single.rule} />
              </Group>
            ) : (
              <Group gap="sm" wrap="nowrap">
                <Text size="sm" c="dimmed">
                  {opened ? 'Hide details' : 'Show details'}
                </Text>
                <Badge color="gray" variant="light">
                  {String(entries.length)}
                </Badge>
              </Group>
            )}
          </Group>
        </InfoAccordion.Control>

        <InfoAccordion.Panel>
          {/* v1 separates the trigger from the content with a top border. */}
          <Divider mb="md" />
          {single ? (
            <EntryDetails entry={single} />
          ) : (
            <Stack gap="md">
              {entries.map((entry, index) => (
                <Fragment key={`${entry.rule_name ?? 'rule'}-${index}`}>
                  {index > 0 && <Divider />}
                  <MultiEntry entry={entry} />
                </Fragment>
              ))}
            </Stack>
          )}
        </InfoAccordion.Panel>
      </InfoAccordion.Item>
    </InfoAccordion>
  )
}
