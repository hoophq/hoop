import { sessionsService } from '@/services/sessions'
import { showSnackbar } from '@/utils/snackbar'
import { CLIENT_DOWNLOAD_LIMIT, buildFilename, matrixToCsv, matrixToJson } from './output'

const MIME = { txt: 'text/plain', csv: 'text/csv', json: 'application/json' }

function triggerDownload(filename, mimeType, content) {
  const url = URL.createObjectURL(new Blob([content], { type: mimeType }))
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  document.body.removeChild(anchor)
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

// The gateway builds the file and answers a signed URL. It refuses (403) when
// downloads are disabled or the user is not the owner, an admin or an auditor.
async function serverDownload(sessionId, format) {
  try {
    const { data } = await sessionsService.get(sessionId, { extension: format })
    window.open(data.download_url, '_blank', 'noopener')
  } catch (err) {
    showSnackbar({
      level: 'error',
      text: 'Failed to generate session file',
      description: err?.response?.data?.message,
    })
  }
}

// format: 'txt' | 'csv' | 'json'. CSV and JSON are offered for a table only.
export function downloadOutput({ output, matrix, sessionId, connectionName, largeOutput }, format) {
  if (sessionId && (largeOutput || output.length > CLIENT_DOWNLOAD_LIMIT)) {
    return serverDownload(sessionId, format)
  }
  const content = { txt: () => output, csv: () => matrixToCsv(matrix), json: () => matrixToJson(matrix) }[format]()
  triggerDownload(buildFilename({ connectionName, sessionId }, format), MIME[format], content)
  return undefined
}
