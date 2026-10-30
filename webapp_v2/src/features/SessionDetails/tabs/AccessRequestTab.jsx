import { Box, Group, Stack, Text } from '@mantine/core'
import { CalendarX2, CircleUser, Users } from 'lucide-react'
import Tooltip from '@/components/Tooltip'
import { formatRelativeTime } from '@/utils/datetime'
import InfoRow from '../components/InfoRow'
import ReviewBadge from '../components/ReviewBadge'
import classes from './tabs.module.css'

const VERBS = { APPROVED: 'approved', REJECTED: 'rejected' }

function Reviewer({ group }) {
  const name = group.reviewed_by?.email || group.reviewed_by?.name
  const verb = VERBS[group.status]
  if (!name || !verb) return null
  return (
    <Group gap={4} wrap="nowrap" c="dimmed">
      <CircleUser size={16} aria-hidden />
      <Text size="sm" fw={500} c="var(--mantine-color-text)">
        {name}
      </Text>
      <Text size="sm">{`${verb} this`}</Text>
      {group.review_date && (
        <Tooltip label={group.review_date}>
          <Text size="sm">{formatRelativeTime(group.review_date)}</Text>
        </Tooltip>
      )}
    </Group>
  )
}

// Figma "Tabs | Access Request": one row per approver group, then who rejected
// the request and why.
export default function AccessRequestTab({ session }) {
  const review = session.review
  const groups = review.review_groups_data || []
  const rejecter = groups.find((group) => group.status === 'REJECTED')?.reviewed_by
  const reason = review.rejection_reason?.trim()

  return (
    <Stack gap="md">
      {groups.map((group) => (
        <InfoRow key={group.id} icon={Users} label={group.group}>
          <Group gap="md" wrap="nowrap">
            <ReviewBadge status={group.status} />
            <Reviewer group={group} />
          </Group>
        </InfoRow>
      ))}
      {review.status === 'REJECTED' && rejecter && (
        <InfoRow icon={CalendarX2} label="Rejected By">
          <Text size="sm" fw={500}>
            {rejecter.email || rejecter.name}
          </Text>
        </InfoRow>
      )}
      {review.status === 'REJECTED' && reason && (
        <Box bg="red.0" p="sm" bdrs="md">
          <Text size="sm" fw={500} c="red.9" mb="xs">
            Comments
          </Text>
          <Text size="sm" c="red.9" className={classes.preWrap}>
            {reason}
          </Text>
        </Box>
      )}
    </Stack>
  )
}
