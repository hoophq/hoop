import { Box, Group, Image, Stack, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import { formatFullDate } from '@/utils/datetime'
import { RiskBadge } from './AiAnalysis'
import { actionLabel, checkStatus } from './helpers'
import InsightCard, { CardSection } from './InsightCard'
import classes from './insights.module.css'

function ResourceCheck({ check }) {
  const status = checkStatus(check.status)
  return (
    <Stack gap={4}>
      <Group gap="xs">
        <Text size="xs" fw={500}>
          Resource check
        </Text>
        <Badge variant="light" color={status.color}>
          {status.label}
        </Badge>
        {check.duration_ms != null && (
          <Text size="xs" c="dimmed">
            {`${check.duration_ms} ms`}
          </Text>
        )}
      </Group>
      {check.reason && (
        <Text size="xs" c="dimmed">
          {check.reason}
        </Text>
      )}
      {check.text && (
        <Box className={classes.trace}>
          <Text size="xs" ff="monospace" className={classes.preWrap}>
            {check.text}
          </Text>
        </Box>
      )}
    </Stack>
  )
}

function Decision({ decision }) {
  const { risk_level: risk, risk_action: action, timestamp, title, explanation, resource_check: check } = decision
  return (
    <Stack gap="xs">
      <Group gap="sm">
        <RiskBadge level={risk} />
        {action && (
          <Text size="xs" fw={500}>
            {actionLabel(action)}
          </Text>
        )}
        {timestamp && (
          <Text size="xs" c="dimmed">
            {formatFullDate(timestamp)}
          </Text>
        )}
      </Group>
      {title && (
        <Text size="sm" fw={500}>
          {title}
        </Text>
      )}
      {explanation && (
        <Text size="xs" className={classes.preWrap}>
          {explanation}
        </Text>
      )}
      {check && <ResourceCheck check={check} />}
    </Stack>
  )
}

// The sidecar analyzer's decision on each statement of a sidecar session.
export default function AiDecisions({ decisions, dropped }) {
  return (
    <InsightCard
      title={
        <Group gap="xs" wrap="nowrap">
          <Image src="/images/ai-session-analyzer-logo.svg" alt="" w={20} h={20} />
          <Text size="sm" fw={700}>
            AI decisions
          </Text>
        </Group>
      }
      summary={(open) => (
        <Group gap="xs" wrap="nowrap">
          <Text size="sm" c="dimmed">
            {open ? 'Hide details' : 'Show details'}
          </Text>
          <Badge variant="light" color="gray">
            {decisions.length + dropped}
          </Badge>
        </Group>
      )}
    >
      {decisions.map((decision, i) =>
        i > 0 ? (
          <CardSection key={decision.seq ?? i}>
            <Decision decision={decision} />
          </CardSection>
        ) : (
          <Decision key={decision.seq ?? i} decision={decision} />
        ),
      )}
      {dropped > 0 && (
        <CardSection>
          <Text size="xs" c="dimmed">
            {`${dropped} more ${dropped === 1 ? 'decision is' : 'decisions are'} not shown.`}
          </Text>
        </CardSection>
      )}
    </InsightCard>
  )
}
