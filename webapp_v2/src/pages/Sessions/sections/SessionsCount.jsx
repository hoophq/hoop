import { Text } from '@mantine/core'
import { useSessionsStore } from '../store'

/**
 * The one piece of the page header that depends on fetched data, split out so a
 * response does not re-render the title and the filter bar with it.
 *
 * It keeps showing the previous numbers while a refetch is in flight (status
 * goes back to 'loading' but `total` is still the old one), so the header never
 * collapses and reflows the page under the user.
 */
export default function SessionsCount() {
  const total = useSessionsStore((s) => s.list.total)
  const loaded = useSessionsStore((s) => s.list.items.length)
  const listStatus = useSessionsStore((s) => s.list.status)
  const idSearchActive = useSessionsStore((s) => s.lookup.status !== 'idle')

  if (idSearchActive) return null
  if (listStatus === 'idle' || (loaded === 0 && listStatus !== 'ready')) return null

  return (
    <Text c="dimmed" size="lg">
      {`Showing ${loaded} of ${total} sessions`}
    </Text>
  )
}
