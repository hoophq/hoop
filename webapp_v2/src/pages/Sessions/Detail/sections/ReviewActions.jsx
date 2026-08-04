import { useEffect, useMemo, useState } from 'react'
import { Group, Menu, Text } from '@mantine/core'
import { CalendarClock, Check, CheckCheck, ChevronDown } from 'lucide-react'
import Button from '@/components/Button'
import { connectionsService } from '@/services/connections'
import { reviewsService } from '@/services/reviews'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'
import { localTimeToUtc, utcTimeToDisplay } from '@/utils/datetime'
import { useSessionsStore } from '../../store'
import RejectDetailsModal from './RejectDetailsModal'
import TimeWindowModal from './TimeWindowModal'

/**
 * Port of the review action bar (session_details.cljs:220-267 and 408-458).
 *
 * Renders only when the current user can review: some review group is PENDING,
 * the user belongs to that group, and the review itself is PENDING.
 */
export default function ReviewActions({ session }) {
  const user = useUserStore((s) => s.user)
  const refreshDetail = useSessionsStore((s) => s.refreshDetail)
  const refreshList = useSessionsStore((s) => s.refreshList)

  const [connection, setConnection] = useState(null)
  const [submitting, setSubmitting] = useState(false)
  const [rejectOpen, setRejectOpen] = useState(false)
  const [timeWindowOpen, setTimeWindowOpen] = useState(false)

  const review = session?.review
  const reviewGroups = review?.review_groups_data ?? []
  const connectionName = session?.connection

  const userGroups = useMemo(() => new Set(user?.groups ?? []), [user])

  const canReview =
    review?.status === 'PENDING' &&
    reviewGroups.some((g) => g.status === 'PENDING' && userGroups.has(g.group))

  // v1 fetches the connection purely to read `force_approve_groups`
  // (session_details.cljs:156-160). Only worth doing when a review exists.
  useEffect(() => {
    if (!review || !connectionName) return
    let cancelled = false
    connectionsService
      .getConnection(connectionName)
      .then((data) => {
        if (!cancelled) setConnection(data)
      })
      .catch(() => {
        // Non-critical: without it, force-approve simply stays hidden.
      })
    return () => {
      cancelled = true
    }
  }, [review, connectionName])

  /**
   * session_details.cljs:226-237. When the connection is loaded AND the review
   * carries no rule name, the force set comes from the connection; otherwise
   * from the review itself.
   */
  const canForceApprove = useMemo(() => {
    if (!canReview) return false
    const forceGroups =
      connection && review?.access_request_rule_name == null
        ? (connection.force_approve_groups ?? [])
        : (review?.force_approval_groups ?? [])
    return forceGroups.some((group) => userGroups.has(group))
  }, [canReview, connection, review, userGroups])

  const submit = async (payload, label) => {
    if (!review?.id) return
    setSubmitting(true)
    try {
      await reviewsService.addReview(review.id, payload)
      showSnackbar({ level: 'success', text: 'Your review was added' })
      // v1 waits 500ms before refetching (events/audit.cljs:560-566) — the
      // gateway propagates the status asynchronously.
      setTimeout(() => {
        refreshDetail()
        refreshList()
      }, 500)
    } catch (error) {
      showSnackbar({
        level: 'error',
        text: 'Failed to add review',
        description: error?.message ?? label,
      })
    } finally {
      setSubmitting(false)
    }
  }

  if (!canReview) return null

  const timeWindow = review?.time_window?.configuration
  const hasTimeWindow = Boolean(timeWindow?.start_time && timeWindow?.end_time)
  const isReady = session?.status === 'ready'

  return (
    <>
      <Group justify="flex-end" gap="sm" align="center">
        {!isReady && session?.verb === 'exec' && hasTimeWindow && (
          <Group gap="xs" align="center" wrap="nowrap">
            <CalendarClock size={16} />
            <Text size="sm" c="dimmed">
              {'This session is set to be executed from '}
              <Text span size="sm" fw={500}>
                {utcTimeToDisplay(timeWindow.start_time)}
              </Text>
              {' to '}
              <Text span size="sm" fw={500}>
                {utcTimeToDisplay(timeWindow.end_time)}
              </Text>
              {'.'}
            </Text>
          </Group>
        )}

        <Button
          color="red"
          variant="light"
          loading={submitting}
          onClick={() => setRejectOpen(true)}
        >
          Reject
        </Button>

        <Menu position="bottom-end" withinPortal>
          <Menu.Target>
            <Button
              color="green"
              loading={submitting}
              rightSection={<ChevronDown size={16} />}
            >
              Approve
            </Button>
          </Menu.Target>
          <Menu.Dropdown>
            {session?.verb !== 'connect' && !hasTimeWindow && (
              <Menu.Item
                rightSection={<CalendarClock size={16} />}
                onClick={() => setTimeWindowOpen(true)}
              >
                Approve in a Time Window
              </Menu.Item>
            )}
            <Menu.Item
              rightSection={<Check size={16} />}
              onClick={() => submit({ status: 'approved' }, 'approve')}
            >
              Approve
            </Menu.Item>
            {canForceApprove && (
              <>
                <Menu.Divider />
                <Menu.Item
                  rightSection={<CheckCheck size={16} />}
                  onClick={() =>
                    submit({ status: 'approved', forceReview: true }, 'force approve')
                  }
                >
                  Force Approve
                </Menu.Item>
              </>
            )}
          </Menu.Dropdown>
        </Menu>
      </Group>

      <RejectDetailsModal
        opened={rejectOpen}
        onClose={() => setRejectOpen(false)}
        onConfirm={({ comment }) => {
          setRejectOpen(false)
          submit({ status: 'rejected', rejectionReason: comment }, 'reject')
        }}
      />

      <TimeWindowModal
        opened={timeWindowOpen}
        onClose={() => setTimeWindowOpen(false)}
        onConfirm={({ startTime, endTime }) => {
          setTimeWindowOpen(false)
          submit(
            {
              status: 'approved',
              // The API stores the window in UTC.
              timeWindow: {
                startTime: localTimeToUtc(startTime),
                endTime: localTimeToUtc(endTime),
              },
            },
            'approve with time window'
          )
        }}
      />
    </>
  )
}
