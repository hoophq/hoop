import { Box, Group, Image, Stack, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import InsightCard from './InsightCard'
import classes from './insights.module.css'

const RISK_COLORS = { low: 'green', medium: 'orange', high: 'red' }
const ACTIONS = {
  block_execution: { color: 'red', label: 'ACTION BLOCKED' },
  allow_execution: { color: 'green', label: 'ACTION ALLOWED' },
}

function RiskBadge({ level }) {
  if (!level) return null
  return (
    <Badge variant="light" color={RISK_COLORS[level] || 'gray'}>
      {`${level.toUpperCase()} RISK`}
    </Badge>
  )
}

// A step type the view does not know draws nothing: `type` is an open string.
function Step({ step }) {
  if (step.type === 'thinking') {
    return (
      <Text size="xs" fs="italic" c="dimmed">
        {step.thinking}
      </Text>
    )
  }
  if (step.type === 'tool_call') {
    return <Text size="xs" ff="monospace">{`▸ ${step.tool_name}(${step.tool_input ?? ''})`}</Text>
  }
  if (step.type === 'tool_result') {
    return (
      <Box className={classes.trace}>
        <Text size="xs" ff="monospace" c={step.is_error ? 'red.7' : 'dimmed'} className={classes.preWrap}>
          {step.tool_output}
        </Text>
      </Box>
    )
  }
  return null
}

// Matrix I01–I03: what the AI Session Analyzer decided about the script.
export default function AiAnalysis({ analysis }) {
  const { risk_level: risk, title, explanation, action, summary, steps = [] } = analysis
  const known = steps.filter((step) => ['thinking', 'tool_call', 'tool_result'].includes(step.type))
  return (
    <InsightCard
      title={
        <Group gap="xs" wrap="nowrap">
          <Image src="/images/ai-session-analyzer-logo.svg" alt="" w={20} h={20} />
          <Text size="sm" fw={700}>
            AI Session Analyzer
          </Text>
        </Group>
      }
      summary={
        <Group gap="xs" wrap="nowrap">
          <Text size="sm" fw={500} truncate>
            {title}
          </Text>
          <RiskBadge level={risk} />
        </Group>
      }
    >
      <Group gap="md">
        <Text size="sm" fw={500}>
          {title}
        </Text>
        <RiskBadge level={risk} />
      </Group>
      {explanation && <Text size="xs">{explanation}</Text>}
      {summary && (
        <Stack gap={4} className={classes.divider}>
          <Text size="xs" fw={500}>
            Summary
          </Text>
          <Text size="xs" c="dimmed">
            {summary}
          </Text>
        </Stack>
      )}
      {known.length > 0 && (
        <Stack gap={4} className={classes.divider}>
          <Text size="xs" fw={500}>
            Investigation trace
          </Text>
          {known.map((step, i) => (
            <Step key={i} step={step} />
          ))}
        </Stack>
      )}
      {ACTIONS[action] && (
        <Box className={classes.divider}>
          <Badge color={ACTIONS[action].color}>
            {ACTIONS[action].label}
          </Badge>
        </Box>
      )}
    </InsightCard>
  )
}
