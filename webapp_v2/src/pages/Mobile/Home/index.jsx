import { useEffect, useState } from 'react'
import { Card, Group, Stack, Text, Title, UnstyledButton } from '@mantine/core'
import { BrainCog, GalleryVerticalEnd, Inbox } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import { agentsService } from '@/services/agents'
import { sessionsService } from '@/services/sessions'
import { startOfTodayISO } from '@/utils/dates'
import MobileHeader from '../components/MobileHeader'

const STAT_ICON_PROPS = { size: 22, color: 'var(--mantine-color-dimmed)', 'aria-hidden': true }

function StatCard({ label, value, hint, icon, accent, onClick }) {
  return (
    <Card withBorder padding="lg" component={UnstyledButton} onClick={onClick}>
      <Group justify="space-between" align="flex-start" wrap="nowrap">
        <Stack gap={4}>
          <Text size="sm" c="dimmed">
            {label}
          </Text>
          <Title order={2} c={accent ? 'indigo.8' : undefined}>
            {value}
          </Title>
          {hint && (
            <Text size="xs" c="dimmed">
              {hint}
            </Text>
          )}
        </Stack>
        {icon}
      </Group>
    </Card>
  )
}

function MobileHome() {
  const navigate = useNavigate()
  const [stats, setStats] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const showLoader = useMinDelay(loading, 500)

  useEffect(() => {
    async function fetchStats() {
      try {
        const [pendingRes, todayRes, agentsRes] = await Promise.all([
          sessionsService.list({ 'review.status': 'PENDING', limit: 1 }),
          sessionsService.list({ start_date: startOfTodayISO(), limit: 1 }),
          agentsService.list(),
        ])
        const agents = agentsRes.data ?? []
        setStats({
          pendingReviews: pendingRes.data?.total ?? 0,
          sessionsToday: todayRes.data?.total ?? 0,
          agentsOnline: agents.filter((a) => a.status === 'CONNECTED').length,
          agentsTotal: agents.length,
        })
      } catch {
        setError('Failed to load overview.')
      } finally {
        setLoading(false)
      }
    }
    fetchStats()
  }, [])

  if (showLoader) return <PageLoader h={400} />
  if (error) return <Text c="red">{error}</Text>

  return (
    <Stack gap="md">
      <MobileHeader title="Home" />
      {stats && (
        <Stack gap="sm">
          <StatCard
            label="Pending reviews"
            value={stats.pendingReviews}
            hint={stats.pendingReviews > 0 ? 'Waiting for your approval' : 'All caught up'}
            icon={<Inbox {...STAT_ICON_PROPS} />}
            accent={stats.pendingReviews > 0}
            onClick={() => navigate('/m/reviews')}
          />
          <StatCard
            label="Sessions today"
            value={stats.sessionsToday}
            icon={<GalleryVerticalEnd {...STAT_ICON_PROPS} />}
            onClick={() => navigate('/m/sessions')}
          />
          <StatCard
            label="Agents online"
            value={`${stats.agentsOnline}/${stats.agentsTotal}`}
            hint={
              stats.agentsOnline < stats.agentsTotal
                ? `${stats.agentsTotal - stats.agentsOnline} offline`
                : 'All agents connected'
            }
            icon={<BrainCog {...STAT_ICON_PROPS} />}
            onClick={() => navigate('/m/agents')}
          />
        </Stack>
      )}
    </Stack>
  )
}

export default MobileHome
