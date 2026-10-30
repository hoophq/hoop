import { useCallback, useMemo } from 'react'
import { terminalRecording } from '../decoders/sshDecoder'
import { decodeByteEvents, terminalText } from '../decoders/terminalDecoder'
import EmptyStream from './recording/EmptyStream'
import LargeRecording from './recording/LargeRecording'
import LogsText from './recording/LogsText'
import TerminalTabs from './recording/TerminalTabs'
import PtyViewer from './PtyViewer'
import RawEvents from './RawEvents'

// The pty channels of an SSH recording. The Logs text comes from the decoded
// channels: the gateway's own text joins the raw frames.
function SshTerminal({ recording }) {
  const decoded = useMemo(() => decodeByteEvents(recording.events), [recording])
  const text = useMemo(() => terminalText(decoded), [decoded])
  const getVideo = useCallback(
    () => ({ width: recording.width, height: recording.height, events: decoded }),
    [recording, decoded],
  )
  return <TerminalTabs logs={text ? <LogsText text={text} /> : <EmptyStream />} getVideo={getVideo} />
}

// An SSH session through the hoop SSH proxy. A stream that is not SSH frames
// was a pty connection when recorded; one without a terminal (a port forward,
// sftp, git) shows its raw events.
export default function SshViewer({ session, largeOutput }) {
  const stream = useMemo(() => session.event_stream || [], [session.event_stream])
  const recording = useMemo(() => terminalRecording(stream), [stream])
  if (largeOutput) return <LargeRecording session={session} />
  if (!recording) return <PtyViewer session={session} largeOutput={false} />
  if (recording.terminal) return <SshTerminal recording={recording} />
  return <RawEvents session={session} showDenials={false} />
}
