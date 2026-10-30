import { Box, Group, Stack, Text, ThemeIcon } from '@mantine/core'
import Badge from '@/components/Badge'
import { ShieldCheck } from 'lucide-react'
import { blockedMessage } from './helpers'
import InsightCard from './InsightCard'
import classes from './insights.module.css'

const RULE_TYPES = {
  pattern_match: { label: 'PATTERN MATCH', color: 'pink' },
  deny_words_list: { label: 'DENY WORD', color: 'red' },
}

function RuleTypeBadge({ type }) {
  const config = RULE_TYPES[type] || { label: (type || 'unknown').toUpperCase(), color: 'gray' }
  return (
    <Badge variant="light" color={config.color}>
      {config.label}
    </Badge>
  )
}

const directionLabel = (direction) => {
  const value = direction || 'input'
  return `${value[0].toUpperCase()}${value.slice(1)} Rule:`
}

function Words({ entry }) {
  const words = entry.matched_words || []
  if (!words.length) return null
  const chips = (
    <Group gap={4}>
      {words.map((word) => (
        <Box key={word} className={classes.chip}>
          {word}
        </Box>
      ))}
    </Group>
  )
  if (entry.rule?.type !== 'pattern_match') return chips
  return (
    <Stack gap="md">
      <Text size="sm" fw={500}>
        Violation:
      </Text>
      {chips}
    </Stack>
  )
}

// The rule's own message (a sidecar rule or a pattern rule may carry one) is
// what the user was told when the statement was blocked.
function Entry({ entry, withName }) {
  return (
    <Stack gap="md">
      <Text size="sm" fw={700}>
        {directionLabel(entry.direction)}
      </Text>
      {withName && (
        <Group gap="xs">
          <Text size="sm" fw={500}>
            {entry.rule_name}
          </Text>
          <RuleTypeBadge type={entry.rule?.type} />
        </Group>
      )}
      <Text size="sm">{blockedMessage(entry.rule?.type, entry.direction, entry.matched_words)}</Text>
      {entry.message && (
        <Text size="sm" c="dimmed">
          {entry.message}
        </Text>
      )}
      <Words entry={entry} />
    </Stack>
  )
}

const title = (
  <Group gap="xs" wrap="nowrap">
    <ThemeIcon size={24} color="blue">
      <ShieldCheck size={16} />
    </ThemeIcon>
    <Text size="sm" fw={700}>
      Guardrails
    </Text>
  </Group>
)

// Matrix I04–I07: the guardrail hits of the session, one card.
export default function Guardrails({ entries }) {
  if (entries.length === 1) {
    const [entry] = entries
    return (
      <InsightCard
        title={title}
        summary={() => (
          <Group gap="xs" wrap="nowrap">
            <Text size="sm" fw={500}>
              {entry.rule_name}
            </Text>
            <RuleTypeBadge type={entry.rule?.type} />
          </Group>
        )}
      >
        <Entry entry={entry} />
      </InsightCard>
    )
  }
  return (
    <InsightCard
      title={title}
      summary={(open) => (
        <Group gap="xs" wrap="nowrap">
          <Text size="sm" c="dimmed">
            {open ? 'Hide details' : 'Show details'}
          </Text>
          <Badge variant="light" color="gray">
            {entries.length}
          </Badge>
        </Group>
      )}
    >
      {entries.map((entry, i) => (
        <Box key={i} className={i > 0 ? classes.divider : undefined}>
          <Entry entry={entry} withName />
        </Box>
      ))}
    </InsightCard>
  )
}
