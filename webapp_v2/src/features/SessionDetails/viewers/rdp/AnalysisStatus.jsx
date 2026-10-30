import { Group, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import { analysisBadges } from './rdpLogic'

// The PII analysis status, its attempts and error, and Retry or Run analysis
// (matrix P08, I12, I13). Nothing renders without a status.
export default function AnalysisStatus({ analysis, retrying, onRetry, showNoEntities = false }) {
  const badges = analysisBadges(analysis)
  if (!badges) return null
  return (
    <Group gap="xs" wrap="wrap">
      <Badge variant="light" color={badges.color} tag>
        {badges.label}
      </Badge>
      {badges.attempt && (
        <Badge variant="light" color="gray" tag>
          {badges.attempt}
        </Badge>
      )}
      {badges.gaveUp && (
        <Badge variant="light" color="red" tag>
          {badges.gaveUp}
        </Badge>
      )}
      {badges.error && (
        <Text size="xs" c="red.8">
          {badges.error}
        </Text>
      )}
      {badges.retry && onRetry && (
        <Button
          size="compact-xs"
          variant="light"
          disabled={retrying}
          onClick={(event) => {
            // It sits in an accordion title: the click must not toggle it.
            event.stopPropagation()
            event.preventDefault()
            onRetry()
          }}
        >
          {retrying ? 'Retrying…' : badges.retry}
        </Button>
      )}
      {showNoEntities && badges.noEntities && (
        <Text size="xs" c="dimmed">
          No PII entities detected.
        </Text>
      )}
    </Group>
  )
}
