import { useState } from 'react'
import { Group } from '@mantine/core'
import { Link2, Square, Workflow } from 'lucide-react'
import Button from '@/components/Button'
import { sessionsService } from '@/services/sessions'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'
import { useSessionsStore } from '../../store'

/**
 * Port of the action bar in `sessions/components/session_header.cljs` (133 LOC).
 *
 * Note this lives in a *separate* CLJS file from `session_details.cljs` — the
 * modal composes the two.
 *
 * Re-run is deliberately absent for now: its script branch routes through the
 * Jira template gate (events/audit.cljs:444-447) and shipping it without that
 * would silently skip a required prompt on connections that have a template.
 * It lands with the jira-templates port.
 */
export default function SessionHeaderActions({ session }) {
  const user = useUserStore((s) => s.user)
  const isAdmin = useUserStore((s) => s.isAdmin)
  const disableClipboard = useUserStore((s) => s.disableClipboard)
  const refreshDetail = useSessionsStore((s) => s.refreshDetail)
  const refreshList = useSessionsStore((s) => s.refreshList)

  // v1 keeps this in a module-level atom shared by every header instance
  // (session_header.cljs:37). Component state is the correct scope.
  const [killing, setKilling] = useState(false)

  if (!session) return null

  const correlationId = session.correlation_id
  const hasWorkflow = Boolean(correlationId && String(correlationId).trim())
  const isOwner = session.user_id === user?.id
  const canKill =
    (isOwner || isAdmin) && session.verb === 'exec' && session.status !== 'done'

  const sessionUrl = `${window.location.origin}/sessions/${encodeURIComponent(session.id)}`

  const kill = async () => {
    setKilling(true)
    try {
      await sessionsService.kill(session.id)
      showSnackbar({ level: 'success', text: 'Session killed successfully' })
      refreshDetail()
      refreshList()
    } catch (error) {
      showSnackbar({
        level: 'error',
        text: 'Failed to kill session',
        description: error?.message,
      })
    } finally {
      setKilling(false)
    }
  }

  const share = async () => {
    try {
      await navigator.clipboard.writeText(sessionUrl)
      showSnackbar({ level: 'success', text: 'URL copied to clipboard' })
    } catch (error) {
      console.error(error)
    }
  }

  return (
    <Group gap="xs" wrap="nowrap">
      {hasWorkflow && (
        <Button
          size="sm"
          variant="light"
          color="gray"
          leftSection={<Workflow size={20} />}
          // In the modal v1 opens the timeline in a new tab rather than
          // navigating, so the session stays open behind it.
          onClick={() =>
            window.open(
              `${window.location.origin}/workflows/${encodeURIComponent(correlationId)}`,
              '_blank'
            )
          }
        >
          View Timeline
        </Button>
      )}

      {canKill && (
        <Button
          size="sm"
          variant="light"
          color="red"
          loading={killing}
          leftSection={<Square size={20} />}
          onClick={kill}
        >
          Kill Session
        </Button>
      )}

      {!disableClipboard && (
        <Button
          size="sm"
          variant="light"
          color="gray"
          leftSection={<Link2 size={20} />}
          onClick={share}
        >
          Share
        </Button>
      )}
    </Group>
  )
}
