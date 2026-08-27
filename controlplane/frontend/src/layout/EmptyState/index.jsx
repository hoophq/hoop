import { Stack, Text, Button, Image, Anchor } from '@mantine/core'

// Two densities. The default owns the whole viewport — it is the only thing on
// screen when a feature has no data yet. `compact` is for an empty result that
// sits inside a page which already has its header, callouts and filters above
// it; there the 50vh block pushes the list controls out of view. Only the
// vertical space changes — the illustration keeps its size in both.
const DENSITY = {
  default: { mih: '50vh', py: 'xxl', gap: 'xl' },
  compact: { mih: undefined, py: 'xl', gap: 'md' },
}

export default function EmptyState({
  title,
  description,
  action,
  docsUrl,
  docsLabel,
  compact = false,
}) {
  const density = DENSITY[compact ? 'compact' : 'default']

  return (
    <Stack flex={1} mih={density.mih} align="center" py={density.py}>
      <Stack flex={1} align="center" justify="center" gap={density.gap}>
        <Image
          src="/images/illustrations/empty-state.png"
          alt=""
          w={320}
          fit="contain"
        />
        <Stack align="center" gap="xs" maw={400}>
          <Text fw={600} c="dimmed" ta="center">{title}</Text>
          {description && (
            <Text size="sm" c="dimmed" ta="center">{description}</Text>
          )}
        </Stack>
        {action && (
          <Button onClick={action.onClick}>{action.label}</Button>
        )}
      </Stack>

      {docsUrl && docsLabel && (
        <Text mt="auto" size="sm" c="dimmed" ta="center">
          {'Need more information? Check out our '}
          <Anchor href={docsUrl} target="_blank" size="sm">
            {docsLabel}
          </Anchor>
          {'.'}
        </Text>
      )}
    </Stack>
  )
}
