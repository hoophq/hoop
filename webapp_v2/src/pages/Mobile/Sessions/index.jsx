import { Fragment, useEffect, useState } from 'react'
import { Card, Center, Divider, Stack, Text } from '@mantine/core'
import { useNavigate } from 'react-router-dom'
import Badge from '@/components/Badge'
import Pagination from '@/components/Pagination'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import { useMinDelay } from '@/hooks/useMinDelay'
import { sessionsService } from '@/services/sessions'
import { timeAgo } from '@/utils/dates'
import MobileHeader from '../components/MobileHeader'
import MobileListCard from '../components/MobileListCard'
import { sessionStatusBadge } from '../statusMaps'

const PAGE_SIZE = 20

function MobileSessions() {
  const navigate = useNavigate()
  const [sessions, setSessions] = useState([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const showLoader = useMinDelay(loading, 500)

  useEffect(() => {
    async function fetchSessions() {
      setLoading(true)
      try {
        const { data } = await sessionsService.list({
          limit: PAGE_SIZE,
          offset: (page - 1) * PAGE_SIZE,
        })
        setSessions(data?.data ?? [])
        setTotal(data?.total ?? 0)
      } catch {
        setError('Failed to load sessions.')
      } finally {
        setLoading(false)
      }
    }
    fetchSessions()
  }, [page])

  if (showLoader) return <PageLoader h={400} />
  if (error) return <Text c="red">{error}</Text>

  const totalPages = Math.ceil(total / PAGE_SIZE)

  return (
    <Stack gap="md">
      <MobileHeader title="Sessions" />
      {sessions.length === 0 ? (
        <EmptyState
          title="No sessions yet"
          description="Sessions show up here once users start connecting."
        />
      ) : (
        <>
          <Card padding={0} withBorder>
            {sessions.map((session, i) => {
              const status = sessionStatusBadge(session.status)
              return (
                <Fragment key={session.id}>
                  {i > 0 && <Divider />}
                  <MobileListCard
                    title={session.user_name || session.user}
                    subtitle={[session.connection, session.verb].filter(Boolean).join(' · ')}
                    meta={timeAgo(session.start_date)}
                    rightSection={<Badge variant={status.variant}>{status.label}</Badge>}
                    onClick={() => navigate(`/m/sessions/${session.id}`)}
                  />
                </Fragment>
              )
            })}
          </Card>
          {totalPages > 1 && (
            <Center>
              <Pagination total={totalPages} value={page} onChange={setPage} siblings={1} size="sm" />
            </Center>
          )}
        </>
      )}
    </Stack>
  )
}

export default MobileSessions
