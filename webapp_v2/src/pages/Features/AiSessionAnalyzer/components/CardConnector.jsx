import { Box, Stack } from '@mantine/core'
import Badge from '@/components/Badge'

// The vertical line between two SettingsCards of one level, 82px tall as in
// the design, with the "Next" pill on it while `next` (the card below is
// closed and not configured). 72px wide, so the line runs under the 40px card
// icon (16px header padding + 20px).
const HEIGHT = 82
const PILL = 24

export default function CardConnector({ next = false }) {
  return (
    <Stack gap={0} align="center" w={72} aria-hidden="true">
      {next ? (
        <>
          <Box w={1} h={(HEIGHT - PILL) / 2} bg="var(--hoop-border-strong)" />
          <Badge variant="light" color="indigo" radius="xl" tag>
            Next
          </Badge>
          <Box w={1} h={(HEIGHT - PILL) / 2} bg="var(--hoop-border-strong)" />
        </>
      ) : (
        <Box w={1} h={HEIGHT} bg="var(--hoop-border-strong)" />
      )}
    </Stack>
  )
}
