import { Group, Text } from '@mantine/core'
import { Braces, Copy, FileText, Sheet, SquareArrowOutUpRight } from 'lucide-react'
import Papa from 'papaparse'
import ActionMenu from '@/components/ActionMenu'
import { sessionsService } from '@/services/sessions'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'

/**
 * Above this size the browser tab is the wrong place to serialize the payload,
 * so the download is delegated to the gateway's signed-URL flow instead
 * (results_download_menu.cljs:10).
 */
const CLIENT_SIDE_THRESHOLD = 2 * 1024 * 1024

const pad2 = (n) => String(n).padStart(2, '0')

/**
 * `YYYYMMDD-HHMMSS` in **UTC** — v1 uses the getUTC* family
 * (results_download_menu.cljs:14-22), so two users in different timezones
 * downloading the same output get the same filename.
 */
function filenameTimestamp() {
  const d = new Date()
  const date = `${d.getUTCFullYear()}${pad2(d.getUTCMonth() + 1)}${pad2(d.getUTCDate())}`
  const time = `${pad2(d.getUTCHours())}${pad2(d.getUTCMinutes())}${pad2(d.getUTCSeconds())}`
  return `${date}-${time}`
}

/** Port of `build-filename` (:24-29) — blank hints are skipped, not rendered empty. */
function buildFilename({ connectionName, sessionId }, ext) {
  const parts = []
  if (connectionName && String(connectionName).trim()) parts.push(connectionName)
  if (sessionId && String(sessionId).trim()) parts.push(sessionId)
  parts.push(filenameTimestamp())
  return `${parts.join('-')}.${ext}`
}

/** Port of `trigger-download!` (:31-40). */
function triggerDownload(filename, mimeType, content) {
  const blob = new Blob([content], { type: mimeType })
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  document.body.removeChild(anchor)
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

/**
 * Port of `matrix->json` (:45-56). A blank head becomes `column_N` (1-based) so
 * the object never carries an empty key, and each row is zipped against the
 * heads — surplus cells on either side are dropped, exactly like `map vector`.
 */
function rowsToJson(heads = [], body = []) {
  const keys = (heads || []).map((head, index) =>
    head == null || !String(head).trim() ? `column_${index + 1}` : head
  )
  const rows = (body || []).map((row) => {
    const entry = {}
    const size = Math.min(keys.length, row?.length ?? 0)
    for (let i = 0; i < size; i += 1) entry[keys[i]] = row[i]
    return entry
  })
  return JSON.stringify(rows, null, 2)
}

/**
 * Dropdown offering output actions (view / copy / download) for a session
 * result. Port of `webapp.components.results-download-menu` (149 LOC).
 *
 * Props:
 *   results          Raw output string already rendered to the user.
 *   tabular          When true, CSV/JSON entries are offered.
 *   heads / body     Parsed matrix split (`first` / `next` of the papaparse
 *                    matrix in v1) used to build CSV and JSON.
 *   sessionId        Filename hint and target of the backend download flow.
 *   connectionName   Filename hint.
 *   hasLargePayload  Forces the backend download flow.
 *   onViewSessionDetails  When provided, renders the "View session details"
 *                    item. The results container never passes it — v1 omits it
 *                    on the session detail surfaces, where it would be a no-op.
 *
 * Past the client-side threshold (and with a sessionId available) downloads are
 * delegated to the gateway token flow instead of being built in the tab.
 */
export default function ResultsDownloadMenu({
  results,
  tabular = false,
  heads,
  body,
  sessionId,
  connectionName,
  hasLargePayload = false,
  onViewSessionDetails,
}) {
  const downloadDisabled = useUserStore((s) => s.disableSessionsDownload)
  const clipboardDisabled = useUserStore((s) => s.disableClipboard)

  const hasContent = typeof results === 'string' && results.trim() !== ''
  const showView = Boolean(onViewSessionDetails)
  const showCopy = hasContent && !clipboardDisabled
  const showDownloads = hasContent && !downloadDisabled

  if (!showView && !showCopy && !showDownloads) return null

  const useBackend =
    Boolean(sessionId) &&
    (Boolean(hasLargePayload) || (hasContent && results.length > CLIENT_SIDE_THRESHOLD))

  const downloadFromBackend = async (format) => {
    try {
      // The gateway answers with a signed URL rather than the bytes.
      const { download_url: url } = await sessionsService.downloadFile(sessionId, format)
      if (url) window.open(url)
    } catch (error) {
      showSnackbar({
        level: 'error',
        text: 'Failed to generate session file',
        description: error?.message,
      })
    }
  }

  const downloadInBrowser = (format) => {
    const meta = { connectionName, sessionId }
    if (format === 'txt') {
      triggerDownload(buildFilename(meta, 'txt'), 'text/plain', results)
      return
    }
    if (format === 'csv') {
      const content =
        tabular && heads ? Papa.unparse([heads, ...(body || [])]) : results
      triggerDownload(buildFilename(meta, 'csv'), 'text/csv', content)
      return
    }
    triggerDownload(buildFilename(meta, 'json'), 'application/json', rowsToJson(heads, body))
  }

  const download = (format) => {
    if (useBackend) downloadFromBackend(format)
    else downloadInBrowser(format)
  }

  // v1 fires the copy without feedback — no snackbar on either outcome.
  const copy = () => {
    navigator.clipboard.writeText(results).catch((error) => console.error(error))
  }

  return (
    // v1 labels this trigger "Output options" (results_download_menu.cljs:114).
    <ActionMenu ariaLabel="Output options">
      {showView && (
        <ActionMenu.Item onClick={onViewSessionDetails}>
          <Group gap="xs" wrap="nowrap">
            <SquareArrowOutUpRight size={16} />
            <Text size="sm">View session details</Text>
          </Group>
        </ActionMenu.Item>
      )}

      {showCopy && (
        <ActionMenu.Item onClick={copy}>
          <Group gap="xs" wrap="nowrap">
            <Copy size={16} />
            <Text size="sm">Copy logs content</Text>
          </Group>
        </ActionMenu.Item>
      )}

      {(showView || showCopy) && showDownloads && <ActionMenu.Divider />}

      {showDownloads && (
        <ActionMenu.Item onClick={() => download('txt')}>
          <Group gap="xs" wrap="nowrap">
            <FileText size={16} />
            <Text size="sm">Download as TXT</Text>
          </Group>
        </ActionMenu.Item>
      )}

      {showDownloads && tabular && (
        <ActionMenu.Item onClick={() => download('csv')}>
          <Group gap="xs" wrap="nowrap">
            <Sheet size={16} />
            <Text size="sm">Download as CSV</Text>
          </Group>
        </ActionMenu.Item>
      )}

      {showDownloads && tabular && (
        <ActionMenu.Item onClick={() => download('json')}>
          <Group gap="xs" wrap="nowrap">
            <Braces size={16} />
            <Text size="sm">Download as JSON</Text>
          </Group>
        </ActionMenu.Item>
      )}
    </ActionMenu>
  )
}
