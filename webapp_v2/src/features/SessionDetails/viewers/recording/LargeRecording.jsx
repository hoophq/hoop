import { Alert, Group, Text } from '@mantine/core'
import Button from '@/components/Button'
import { Download, Info } from 'lucide-react'
import { sessionsService } from '@/services/sessions'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'

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
    <Alert variant="light" color="gray" icon={<Info size={16} />}>
      <Group justify="space-between" wrap="nowrap">
        <Text size="sm">This recording is too large to display</Text>
        {!disableDownload && (
          <Button size="sm" variant="light" color="gray" rightSection={<Download size={16} />} onClick={() => download(session.id)}>
            Download
          </Button>
        )}
      </Group>
    </Alert>
  )
}
