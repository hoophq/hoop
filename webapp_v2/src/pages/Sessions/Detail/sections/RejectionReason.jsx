import { Box, Group, Stack, Text } from '@mantine/core'
import { OctagonX } from 'lucide-react'

/**
 * Port of `sessions/components/rejection_reason.cljs` (27 LOC) — the red panel
 * shown at the top of the session detail when the access request was rejected.
 *
 * v1 renders nothing unless the review status is REJECTED, and picks the
 * reviewer from the first review group whose own status is REJECTED (a request
 * is rejected as soon as one group rejects it, so the first match is the
 * rejecting group).
 */
export default function RejectionReason({ session }) {
  const review = session?.review
  if (review?.status !== 'REJECTED') return null

  const reason = review.rejection_reason
  const reviewer = review.review_groups_data?.find((group) => group.status === 'REJECTED')
    ?.reviewed_by?.email

  // v1 uses `whitespace-pre-wrap`; splitting on newlines keeps the author's line
  // breaks without reaching for a CSS Module for a single rule.
  const reasonLines = typeof reason === 'string' && reason.trim() !== '' ? reason.split('\n') : []

  return (
    <Box bg="red.0" p="md" radius="md">
      <Group justify="space-between" align="flex-start" gap="md" wrap="nowrap">
        <Group gap="xs" wrap="nowrap">
          <OctagonX size={16} color="var(--mantine-color-red-9)" />
          <Text size="sm" fw={700} c="red.9">
            Reject Details
          </Text>
        </Group>

        <Stack align="flex-end" gap="xs">
          {reasonLines.length > 0 && (
            <Stack align="flex-end" gap={0}>
              {reasonLines.map((line, index) => (
                <Text key={index} size="sm" c="red.9" ta="right">
                  {line}
                </Text>
              ))}
            </Stack>
          )}

          {reviewer && (
            <Text size="sm" fw={700} c="red.9">
              {`Rejected by ${reviewer}`}
            </Text>
          )}
        </Stack>
      </Group>
    </Box>
  )
}
