import { useEffect, useState, useSyncExternalStore } from 'react'
import { Group, Loader, Menu, Text } from '@mantine/core'
import { CalendarClock, Check, CheckCheck, ChevronDown } from 'lucide-react'
import Button from '@/components/Button'
import { useNativeAccessStore } from '@/stores/useNativeAccessStore'
import { useNativeConnectionsStore } from '@/stores/useNativeConnectionsStore'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'
import { useUserStore } from '@/stores/useUserStore'
import { formatFullDate, isWithinTimeWindow, utcTimeToDisplay } from '@/utils/datetime'
import { getTick, subscribeTick } from '@/utils/tick'
import { canReview, hasReview, isRunningAsync } from '../sessionState'
import RejectModal from './RejectModal'
import TimeWindowModal from './TimeWindowModal'

// The gateway reports the access duration of a review in nanoseconds.
const NS_PER_SECOND = 1e9

function Bar({ className, children }) {
  return (
    <Group justify="flex-end" gap="sm" wrap="nowrap" className={className}>
      {children}
    </Group>
  )
}

const Helper = ({ children }) => (
  <Text size="sm" c="dimmed">
    {children}
  </Text>
)

function windowOf(session) {
  const { start_time: start, end_time: end } = session.review?.time_window?.configuration || {}
  return start && end ? { start, end } : null
}

function ReviewActions({ session, className }) {
  const user = useUserStore((s) => s.user)
  const decide = useSessionDetailsStore((s) => s.decide)
  const deciding = useSessionDetailsStore((s) => s.deciding)
  const setStacked = useSessionDetailsStore((s) => s.setStacked)
  const [rejecting, setRejecting] = useState(false)
  const [scheduling, setScheduling] = useState(false)
  useEffect(() => {
    setStacked(rejecting || scheduling)
    return () => setStacked(false)
  }, [rejecting, scheduling, setStacked])
  const timeWindow = windowOf(session)
  const review = session.review
  // A sidecar review (it names a listener) takes no time window.
  const canSchedule = session.verb !== 'connect' && !review.listener_name && !timeWindow
  const forceGroups = useSessionDetailsStore((s) => s.forceGroups)
  const canForce = (user?.groups || []).some((group) => forceGroups.includes(group))

  return (
    <Bar className={className}>
      {timeWindow && session.verb === 'exec' && (
        <Group gap="xs" wrap="nowrap">
          <CalendarClock size={16} color="var(--mantine-color-dimmed)" aria-hidden />
          <Helper>
            {`This session is set to be executed from ${utcTimeToDisplay(timeWindow.start)} to ${utcTimeToDisplay(timeWindow.end)}.`}
          </Helper>
        </Group>
      )}
      <Button variant="light" color="red" onClick={() => setRejecting(true)} disabled={deciding}>
        Reject
      </Button>
      <Menu position="top-end" withinPortal>
        <Menu.Target>
          <Button color="green" loading={deciding} rightSection={<ChevronDown size={16} />}>
            Approve
          </Button>
        </Menu.Target>
        <Menu.Dropdown>
          {canSchedule && (
            <Menu.Item rightSection={<CalendarClock size={16} />} onClick={() => setScheduling(true)}>
              Approve in a Time Window
            </Menu.Item>
          )}
          <Menu.Item rightSection={<Check size={16} />} onClick={() => decide({ status: 'APPROVED' })}>
            Approve
          </Menu.Item>
          {canForce && (
            <>
              <Menu.Divider />
              <Menu.Item
                rightSection={<CheckCheck size={16} />}
                onClick={() => decide({ status: 'APPROVED', forceReview: true })}
              >
                Force Approve
              </Menu.Item>
            </>
          )}
        </Menu.Dropdown>
      </Menu>
      <RejectModal
        opened={rejecting}
        loading={deciding}
        onClose={() => setRejecting(false)}
        onConfirm={async (reason) => {
          if (await decide({ status: 'REJECTED', rejectionReason: reason })) setRejecting(false)
        }}
      />
      <TimeWindowModal
        opened={scheduling}
        loading={deciding}
        onClose={() => setScheduling(false)}
        onConfirm={async (window) => {
          if (await decide({ status: 'APPROVED', timeWindow: window })) setScheduling(false)
        }}
      />
    </Bar>
  )
}

function ExecuteActions({ session, owner, className }) {
  const execute = useSessionDetailsStore((s) => s.execute)
  const execution = useSessionDetailsStore((s) => s.executions[session.id])
  const now = useSyncExternalStore(subscribeTick, getTick)
  const timeWindow = windowOf(session)
  const reviewStatus = session.review?.status
  const approved = !hasReview(session) || reviewStatus === 'APPROVED'

  if (isRunningAsync(session, execution)) {
    return (
      <Bar className={className}>
        <Loader size="xs" />
        <Helper>Execution in progress. The result will appear here when it finishes.</Helper>
      </Bar>
    )
  }
  // Figma "Non-Approver - Standard User": the owner sees why Execute waits.
  if (hasReview(session) && reviewStatus === 'REJECTED' && owner) {
    return (
      <Bar className={className}>
        <Helper>This session is rejected</Helper>
        <Button disabled>Execute</Button>
      </Bar>
    )
  }
  if (hasReview(session) && reviewStatus === 'PENDING' && owner) {
    return (
      <Bar className={className}>
        <Helper>This session is waiting for review</Helper>
        <Button disabled>Execute</Button>
      </Bar>
    )
  }
  if (session.status !== 'ready' || !approved) return null
  if (timeWindow) {
    // Checked every second: the button enables when the window opens.
    const within = isWithinTimeWindow(timeWindow.start, timeWindow.end, new Date(now))
    return (
      <Bar className={className}>
        <Helper>
          {`This session is ready and available to be executed from ${utcTimeToDisplay(timeWindow.start)} to ${utcTimeToDisplay(timeWindow.end)}.`}
        </Helper>
        {owner && (
          <Button disabled={!within} onClick={execute}>
            Execute
          </Button>
        )}
      </Bar>
    )
  }
  if (!owner) return null
  return (
    <Bar className={className}>
      <Helper>This session is ready to be executed.</Helper>
      <Button onClick={execute}>Execute</Button>
    </Bar>
  )
}

// An approved request for native credentials (verb connect).
function ConnectActions({ session, className }) {
  const now = useSyncExternalStore(subscribeTick, getTick)
  const active = useNativeAccessStore((s) => s.activeByName[session.connection])
  const resumeAfterReview = useNativeAccessStore((s) => s.resumeAfterReview)
  const openConnection = useNativeConnectionsStore((s) => s.openConnection)
  const close = useSessionDetailsStore((s) => s.close)
  const metadata = session.metadata || {}
  const revokedAt = metadata.credentials_revoked_at
  const expireAt = metadata.credentials_expire_at
  const expired = expireAt && new Date(expireAt).getTime() <= now
  const remainingMin = expireAt && !expired ? Math.max(1, Math.ceil((new Date(expireAt).getTime() - now) / 60000)) : null
  const minutes = remainingMin && `Credentials valid for ${remainingMin} minute${remainingMin === 1 ? '' : 's'}.`

  if (revokedAt) {
    return (
      <Bar className={className}>
        <Helper>{`Your credentials have been revoked at ${formatFullDate(revokedAt)}.`}</Helper>
      </Bar>
    )
  }
  if (expired) {
    return (
      <Bar className={className}>
        <Helper>{`Your credentials have expired at ${formatFullDate(expireAt)}.`}</Helper>
      </Bar>
    )
  }
  if (active) {
    return (
      <Bar className={className}>
        <Helper>{minutes || 'You already have active credentials for this connection.'}</Helper>
        <Button
          color="green"
          onClick={() => {
            close()
            openConnection(session.connection)
          }}
        >
          View Credentials
        </Button>
      </Bar>
    )
  }
  if (session.status === 'done') {
    return (
      <Bar className={className}>
        <Helper>This session has ended. Use Native Connections to connect again.</Helper>
      </Bar>
    )
  }
  const accessDurationSec = session.review?.access_duration
    ? Math.round(session.review.access_duration / NS_PER_SECOND)
    : undefined
  return (
    <Bar className={className}>
      <Helper>
        {minutes
          ? `${minutes} Click Connect to rotate credentials.`
          : 'Your access has been approved. Click Connect to obtain credentials.'}
      </Helper>
      <Button
        color="green"
        onClick={() => {
          close()
          openConnection(session.connection)
          resumeAfterReview(session.connection, session.id, accessDurationSec)
        }}
      >
        Connect
      </Button>
    </Bar>
  )
}

// Figma "Main Action Bar | Footer": the main action for the user's role and
// the state of the request. Nothing renders when there is no action.
export default function ActionBar({ session, className }) {
  const user = useUserStore((s) => s.user)
  const owner = Boolean(session.user_id) && session.user_id === user?.id

  if (canReview(session, user)) return <ReviewActions session={session} className={className} />
  if (session.verb === 'exec') return <ExecuteActions session={session} owner={owner} className={className} />
  if (session.verb === 'connect' && session.status !== 'open' && hasReview(session) && owner) {
    return <ConnectActions session={session} className={className} />
  }
  return null
}
