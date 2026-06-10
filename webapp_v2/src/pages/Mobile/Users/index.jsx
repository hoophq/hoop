import { Fragment, useEffect, useState } from 'react'
import { Card, Divider, Stack, Text } from '@mantine/core'
import { Search } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import Badge from '@/components/Badge'
import TextInput from '@/components/TextInput'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import { useMinDelay } from '@/hooks/useMinDelay'
import { usersService } from '@/services/users'
import MobileHeader from '../components/MobileHeader'
import MobileListCard from '../components/MobileListCard'
import { statusVariant } from './statusVariant'

function MobileUsers() {
  const navigate = useNavigate()
  const [users, setUsers] = useState([])
  const [search, setSearch] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(null)
  const showLoader = useMinDelay(loading, 500)

  useEffect(() => {
    async function fetchUsers() {
      try {
        const { data } = await usersService.list()
        setUsers(data ?? [])
      } catch {
        setError('Failed to load users.')
      } finally {
        setLoading(false)
      }
    }
    fetchUsers()
  }, [])

  if (showLoader) return <PageLoader h={400} />
  if (error) return <Text c="red">{error}</Text>

  const term = search.trim().toLowerCase()
  const filtered = [...users]
    .filter(
      (u) =>
        !term ||
        (u.name ?? '').toLowerCase().includes(term) ||
        (u.email ?? '').toLowerCase().includes(term),
    )
    .sort((a, b) => (a.name ?? '').localeCompare(b.name ?? ''))

  return (
    <Stack gap="md">
      <MobileHeader title="Users" />
      <TextInput
        placeholder="Search by name or email"
        value={search}
        onChange={(e) => setSearch(e.currentTarget.value)}
        leftSection={<Search size={16} />}
      />
      {filtered.length === 0 ? (
        <EmptyState
          title={term ? 'No users found' : 'No users yet'}
          description={term ? `No user matches "${term}".` : 'Users show up here once they join your organization.'}
        />
      ) : (
        <Card padding={0} withBorder>
          {filtered.map((user, i) => (
            <Fragment key={user.id}>
              {i > 0 && <Divider />}
              <MobileListCard
                title={user.name || user.email}
                subtitle={user.name ? user.email : null}
                rightSection={<Badge variant={statusVariant(user.status)}>{user.status ?? '—'}</Badge>}
                onClick={() => navigate(`/m/users/${user.id}`)}
              />
            </Fragment>
          ))}
        </Card>
      )}
    </Stack>
  )
}

export default MobileUsers
