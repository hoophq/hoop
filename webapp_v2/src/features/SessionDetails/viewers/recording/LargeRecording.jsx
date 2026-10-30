import { sessionsService } from '@/services/sessions'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'
import PreviewUnavailable from '../../components/PreviewUnavailable'

async function download(sessionId) {
  try {
    const { data } = await sessionsService.get(sessionId, { extension: 'txt' })
    window.open(data.download_url, '_blank', 'noopener')
  } catch (err) {
    showSnackbar({ level: 'error', text: 'Failed to generate session file', description: err?.response?.data?.message })
  }
}

// Figma "Preview Not Available - Download": a recording above the 4 MB the
// details expand is not drawn here.
export default function LargeRecording({ session }) {
  const disableDownload = useUserStore((s) => s.disableSessionsDownload)
  return (
    <PreviewUnavailable
      size={session.event_size}
      message="This recording is too large to display"
      onDownload={disableDownload ? undefined : () => download(session.id)}
    />
  )
}
