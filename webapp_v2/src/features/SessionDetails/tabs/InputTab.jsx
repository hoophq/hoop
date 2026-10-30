import { Alert, Group, Text } from '@mantine/core'
import { Download, Info } from 'lucide-react'
import Button from '@/components/Button'
import CodeSnippet from '@/components/CodeSnippet'
import { sessionsService } from '@/services/sessions'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'

async function downloadInput(sessionId) {
  try {
    const { data } = await sessionsService.get(sessionId, { 'blob-type': 'session_input', extension: 'txt' })
    window.open(data.download_url, '_blank', 'noopener')
  } catch (err) {
    showSnackbar({ level: 'error', text: 'Failed to download session input', description: err?.response?.data?.message })
  }
}

// Figma "Input Tab": the script as plain text, or "Preview not available" with
// a download when it is above the 4 MB the gateway expands.
export default function InputTab({ session }) {
  const largeInput = useSessionDetailsStore((s) => s.largeInput)
  const disableDownload = useUserStore((s) => s.disableSessionsDownload)

  if (largeInput) {
    return (
      <Alert variant="light" color="gray" icon={<Info size={16} />}>
        <Group justify="space-between" wrap="nowrap">
          <Text size="sm">Input script is too large to display</Text>
          {!disableDownload && (
            <Button
              size="sm"
              variant="light"
              color="gray"
              rightSection={<Download size={16} />}
              onClick={() => downloadInput(session.id)}
            >
              Download
            </Button>
          )}
        </Group>
      </Alert>
    )
  }

  return <CodeSnippet code={session.script?.data || ''} />
}
