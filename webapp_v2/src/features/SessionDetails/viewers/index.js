import { lazy } from 'react'

// One viewer per value of decoders/sessionFormat#outputViewer. Each takes
// { session, largeOutput } and draws its own view options.
export const VIEWERS = {
  exec: lazy(() => import('./ExecOutput')),
  live: lazy(() => import('./LiveTail')),
  rdp: lazy(() => import('./RdpPlayer')),
  ssh: lazy(() => import('./SshViewer')),
  pty: lazy(() => import('./PtyViewer')),
  raw: lazy(() => import('./RawEvents')),
}
