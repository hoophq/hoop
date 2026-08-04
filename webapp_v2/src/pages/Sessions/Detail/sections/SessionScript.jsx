import { Alert, Box, Group, ScrollArea, Text } from '@mantine/core'
import { Download, Info } from 'lucide-react'
import Button from '@/components/Button'
import Code from '@/components/Code'
import { sessionsService } from '@/services/sessions'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'

/**
 * Port of the script area (session_details.cljs:336-346) and
 * `large-input-warning` (:46-65).
 *
 * When the input exceeds the 4 MB threshold the gateway never sends it, so v1
 * swaps the script for a callout offering a download instead. The download
 * button is hidden when the gateway sets `disable_sessions_download`.
 */
export default function SessionScript({ session, hasLargeInput }) {
  const disableSessionsDownload = useUserStore((s) => s.disableSessionsDownload)

  const scriptData = session?.script?.data
  if (!hasLargeInput && !scriptData) return null

  const download = async () => {
    try {
      // The gateway answers with a signed URL rather than the bytes.
      const { download_url: url } = await sessionsService.downloadInput(session.id)
      if (url) window.open(url)
    } catch (error) {
      showSnackbar({
        level: 'error',
        text: 'Failed to download session input',
        description: error?.message,
      })
    }
  }

  if (hasLargeInput) {
    return (
      <Alert variant="light" color="gray" icon={<Info size={16} />}>
        <Group justify="space-between" align="center" gap="lg" wrap="nowrap">
          <Text size="sm">Input script is too large to display</Text>
          {!disableSessionsDownload && (
            <Button
              size="sm"
              variant="light"
              rightSection={<Download size={16} />}
              onClick={download}
            >
              Download
            </Button>
          )}
        </Group>
      </Alert>
    )
  }

  return (
    <Box>
      {/* v1 caps the script at 160px and scrolls — a soft truncation with no warning. */}
      <ScrollArea.Autosize mah={160}>
        <Code block>{scriptData}</Code>
      </ScrollArea.Autosize>
    </Box>
  )
}
