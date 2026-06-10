import { Fragment, useEffect, useState } from 'react'
import { Card, Divider, Stack, Text } from '@mantine/core'
import { useNavigate } from 'react-router-dom'
import Badge from '@/components/Badge'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import { useMinDelay } from '@/hooks/useMinDelay'
import { sessionsService } from '@/services/sessions'
import { timeAgo } from '@/utils/dates'
import MobileHeader from '../components/MobileHeader'
import MobileListCard from '../components/MobileListCard'

function MobileReviews() {
  const navigate = useNavigate()
  const [sessions, setSessions] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const showLoader = useMinDelay(loading, 500)

  useEffect(() => {
    async function fetchPending() {
      try {
        const { data } = await sessionsService.list({ 'review.status': 'PENDING', limit: 50 })
        setSessions(data?.data ?? [])
      } catch {
        setError('Failed to load reviews.')
      } finally {
        setLoading(false)
      }
    }
    fetchPending()
  }, [])

  if (showLoader) return <PageLoader h={400} />
  if (error) return <Text c="red">{error}</Text>

  return (
    <Stack gap="md">
      <MobileHeader title="Reviews" />
      {sessions.length === 0 ? (
        <EmptyState
          title="No pending reviews"
          description="Sessions waiting for approval show up here."
        />
      ) : (
        <Card padding={0} withBorder>
          {sessions.map((session, i) => (
            <Fragment key={session.id}>
              {i > 0 && <Divider />}
              <MobileListCard
                title={session.user_name || session.user}
                subtitle={[session.connection, session.verb].filter(Boolean).join(' · ')}
                meta={
                  session.review?.type === 'jit'
                    ? `${timeAgo(session.start_date)} · JIT access`
                    : timeAgo(session.start_date)
                }
                rightSection={<Badge variant="warning">Pending</Badge>}
                onClick={() => navigate(`/m/reviews/${session.id}`)}
              />
            </Fragment>
          ))}
        </Card>
      )}
    </Stack>
  )
}

export default MobileReviews
