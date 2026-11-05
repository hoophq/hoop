import { useCallback, useEffect, useMemo, useState } from 'react'
import { Group, Loader, Text } from '@mantine/core'
import { sessionsService } from '@/services/sessions'
import { showSnackbar } from '@/utils/snackbar'
import { b64ToText, decodeEvents } from '../decoders/terminalDecoder'
import EmptyStream from './recording/EmptyStream'
import LargeRecording from './recording/LargeRecording'
import LogsText from './recording/LogsText'
import TerminalTabs from './recording/TerminalTabs'

// A pty recording holds no terminal size.
const PTY_SIZE = { width: 80, height: 24 }

function LoadingLogs() {
  return (
    <Group justify="center" gap="sm" py="xl">
      <Text size="xs" c="dimmed" fs="italic">
        Loading logs for this session
      </Text>
      <Loader size="xs" />
    </Group>
  )
}

// The Logs text is the output the gateway joins: read once per session.
function useSessionLogs(sessionId) {
  const [logs, setLogs] = useState({ id: null, status: 'loading', text: '' })
  useEffect(() => {
    let current = true
    sessionsService
      .get(sessionId, { expand: 'event_stream,session_input', event_stream: 'base64' })
      .then(({ data }) => {
        if (current) setLogs({ id: sessionId, status: 'ready', text: b64ToText(data?.event_stream?.[0] || '') })
      })
      .catch((err) => {
        if (!current) return
        showSnackbar({ level: 'error', text: 'Failed to load session logs', description: err?.response?.data?.message })
        setLogs({ id: sessionId, status: 'error', text: '' })
      })
    return () => {
      current = false
    }
  }, [sessionId])
  // Another session's logs are still loading.
  return logs.id === sessionId ? logs : { status: 'loading', text: '' }
}

function PtyRecording({ session, stream }) {
  const logs = useSessionLogs(session.id)
  const getVideo = useCallback(() => ({ ...PTY_SIZE, events: decodeEvents(stream) }), [stream])
  let view = <EmptyStream />
  if (logs.status === 'loading') view = <LoadingLogs />
  else if (logs.text) view = <LogsText text={logs.text} />
  return <TerminalTabs logs={view} getVideo={getVideo} />
}

// Figma "Output | Logs" and "Output | Video" for a pty session (a shell, a
// kubectl exec): the output with its colors, and a replay.
export default function PtyViewer({ session, largeOutput }) {
  const stream = useMemo(() => session.event_stream || [], [session.event_stream])
  if (largeOutput) return <LargeRecording session={session} />
  if (!stream.length) return <EmptyStream />
  return <PtyRecording session={session} stream={stream} />
}
