import { useState } from 'react'
import EmptyState from '@/layout/EmptyState'
import RdpStreamingPlayer from './rdp/RdpStreamingPlayer'
import RdpSummary from './rdp/RdpSummary'
import { recordingSize } from './rdp/rdpLogic'

// An RDP recording: its summary, then the player once the user asks for it.
// The frames come from /rdp-frames, not from the session's event stream.
export default function RdpPlayer({ session }) {
  const [playing, setPlaying] = useState(false)
  const metrics = session.metrics || {}
  const bitmaps = metrics.bitmap_count || 0
  const { width, height } = recordingSize(metrics)

  if (playing) return <RdpStreamingPlayer sessionId={session.id} width={width} height={height} />
  if (bitmaps > 0) {
    return (
      <RdpSummary
        bitmaps={bitmaps}
        bytes={metrics.event_size ?? session.event_size ?? 0}
        width={width}
        height={height}
        onPlay={() => setPlaying(true)}
      />
    )
  }
  return (
    <EmptyState
      compact
      title="This session needs approval or execution."
      description="Or maybe it's still running and you just need to wait it to end."
    />
  )
}
