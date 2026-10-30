import { Box, Stack } from '@mantine/core'
import Badge from '@/components/Badge'

// The vertical line between two SettingsCards of one level, with the "Next"
// pill on it. 72px wide, so the line runs under the 40px card icon (16px
// header padding + 20px).
export default function CardConnector() {
  return (
    <Stack gap={0} align="center" w={72} aria-hidden="true">
      <Box w={1} h={12} bg="gray.2" />
      <Badge variant="light" color="indigo" radius="xl" tag>
        Next
      </Badge>
      <Box w={1} h={12} bg="gray.2" />
    </Stack>
  )
}
