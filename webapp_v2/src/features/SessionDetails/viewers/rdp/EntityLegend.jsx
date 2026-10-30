import { Group, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import AnalysisStatus from './AnalysisStatus'
import { entityColor } from './rdpLogic'
import classes from './Rdp.module.css'

// "PII highlights": the analysis status and one toggle per entity type found
// (matrix P08). Nothing renders before there is something to say.
export default function EntityLegend({ loading, analysis, retrying, onRetry, types, enabledTypes, onToggle }) {
  if (!loading && !types.length && !analysis.status) return null
  return (
    <Group gap="xs" wrap="wrap" px="md" py="sm" className={classes.legend}>
      <Text size="xs" fw={500} tt="uppercase" c="dimmed" className={classes.legendLabel}>
        PII highlights
      </Text>
      <AnalysisStatus analysis={analysis} retrying={retrying} onRetry={onRetry} showNoEntities />
      {loading && (
        <Badge variant="light" color="blue" tag>
          Loading detections...
        </Badge>
      )}
      {types.map((type) => {
        const enabled = enabledTypes.has(type)
        return (
          <Badge
            key={type}
            component="button"
            type="button"
            color={entityColor(type)}
            variant={enabled ? 'light' : 'outline'}
            opacity={enabled ? 1 : 0.6}
            aria-pressed={enabled}
            classNames={{ root: classes.entityToggle }}
            onClick={() => onToggle(type)}
          >
            {type}
          </Badge>
        )
      })}
    </Group>
  )
}
