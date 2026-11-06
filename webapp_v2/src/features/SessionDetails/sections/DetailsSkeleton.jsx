import { Group, Skeleton, Stack } from '@mantine/core'

const ROWS = [40, 60, 80]

// Figma "Skeleton": the shape of the information module, the tabs and the body.
export default function DetailsSkeleton() {
  return (
    <Stack gap="lgAlt" aria-busy="true" aria-label="Loading session">
      <Stack gap="md">
        {ROWS.map((width) => (
          <Group key={width} gap="smAlt" wrap="nowrap">
            <Skeleton h={24} w={160} />
            <Skeleton h={24} w={`${width}%`} maw={250} />
          </Group>
        ))}
        <Skeleton h={24} w={160} />
      </Stack>
      <Group gap="md">
        {[0, 1, 2, 3].map((i) => (
          <Skeleton key={i} h={24} w={75} />
        ))}
      </Group>
      <Skeleton h={200} />
    </Stack>
  )
}
