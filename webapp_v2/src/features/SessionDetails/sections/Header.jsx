import { useNavigate } from 'react-router-dom'
import { Group, Title } from '@mantine/core'
import { Link2, Square, Workflow, X } from 'lucide-react'
import ActionIcon from '@/components/ActionIcon'
import Button from '@/components/Button'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'
import { isSidecarReview } from '../sessionState'
import RerunAction from './RerunAction'

const SOFT = { variant: 'light', color: 'gray', size: 'sm' }

// Title and session actions (Figma "Fixed Header"). While the session loads,
// the actions wait: a stub from the bridge would show Kill and Re-run to an
// admin for a moment.
export default function Header({ session, ready, onClose }) {
  const navigate = useNavigate()
  const user = useUserStore((s) => s.user)
  const isAdmin = useUserStore((s) => s.isAdmin)
  const disableClipboard = useUserStore((s) => s.disableClipboard)
  const kill = useSessionDetailsStore((s) => s.kill)
  const killing = useSessionDetailsStore((s) => s.killing)

  const exec = session?.verb === 'exec'
  const owner = session?.user_id && session.user_id === user?.id
  const canKill = ready && exec && (owner || isAdmin) && session.status !== 'done' && !isSidecarReview(session)
  const canRerun = ready && exec && !session.integrations_metadata?.jira_issue_url && !isSidecarReview(session)
  const correlationId = session?.correlation_id?.trim()

  const share = async () => {
    const url = `${window.location.origin}/sessions/${encodeURIComponent(session.id)}`
    try {
      await navigator.clipboard.writeText(url)
      showSnackbar({ level: 'success', text: 'URL copied to clipboard' })
    } catch {
      showSnackbar({ level: 'error', text: 'Failed to copy the URL' })
    }
  }

  const openTimeline = () => {
    onClose()
    navigate(`/workflows/${encodeURIComponent(correlationId)}`)
  }

  return (
    <Group justify="space-between" align="flex-start" wrap="nowrap" gap="sm">
      <Title order={2} fz={24} lh="30px">
        Session Details
      </Title>
      <Group gap="sm" wrap="nowrap">
        {ready && correlationId && (
          <Button {...SOFT} leftSection={<Workflow size={16} />} onClick={openTimeline}>
            View Timeline
          </Button>
        )}
        {canKill && (
          <Button {...SOFT} color="red" leftSection={<Square size={16} />} loading={killing} onClick={kill}>
            Kill Session
          </Button>
        )}
        {canRerun && <RerunAction session={session} buttonProps={SOFT} />}
        {ready && !disableClipboard && (
          <Button {...SOFT} leftSection={<Link2 size={16} />} onClick={share}>
            Share
          </Button>
        )}
        <ActionIcon variant="light" color="gray" size="sm" aria-label="Close" onClick={onClose}>
          <X size={16} />
        </ActionIcon>
      </Group>
    </Group>
  )
}

