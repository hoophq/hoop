import { Stack } from '@mantine/core'
import { Download } from 'lucide-react'
import Button from '@/components/Button'
import CodeSnippet from '@/components/CodeSnippet'
import { sessionsService } from '@/services/sessions'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'
import PreviewUnavailable from '../components/PreviewUnavailable'
import Insights from '../insights/Insights'
import { canDownloadInput } from '../sessionState'
import ViewOptions from '../components/ViewOptions'

async function downloadInput(sessionId) {
  try {
    const { data } = await sessionsService.get(sessionId, { 'blob-type': 'session_input', extension: 'txt' })
    window.open(data.download_url, '_blank', 'noopener')
  } catch (err) {
    showSnackbar({ level: 'error', text: 'Failed to download session input', description: err?.response?.data?.message })
  }
}

// Figma "Input Tab": the script as plain text with Download, or "Preview not
// available" with a download when it is above the 4 MB the gateway expands.
export default function InputTab({ session }) {
  const largeInput = useSessionDetailsStore((s) => s.largeInput)
  const disableDownload = useUserStore((s) => s.disableSessionsDownload)
  const user = useUserStore((s) => s.user)
  const isAdmin = useUserStore((s) => s.isAdmin)
  const download = () => downloadInput(session.id)
  // The large-input card offers Download to every user, as the ClojureScript
  // one did (matrix FD16); the gateway refuses who it does not serve.
  const onLargeDownload = disableDownload ? undefined : download
  const onDownload = !disableDownload && canDownloadInput(session, { user, isAdmin }) ? download : undefined

  const insights = <Insights session={session} tabs={['input']} />

  if (largeInput) {
    return (
      <Stack gap="lgAlt">
        {insights}
        <PreviewUnavailable
          title="Session Input"
          size={session.script_size}
          message="Input script is too large to display"
          onDownload={onLargeDownload}
        />
      </Stack>
    )
  }

  return (
    <Stack gap="md">
      {insights}
      {onDownload && (
        <ViewOptions>
          <Button size="sm" variant="light" color="gray" leftSection={<Download size={16} />} onClick={onDownload}>
            Download
          </Button>
        </ViewOptions>
      )}
      <CodeSnippet code={session.script?.data || ''} />
    </Stack>
  )
}
