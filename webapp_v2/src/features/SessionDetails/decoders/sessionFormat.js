// Subtypes the gateway serves through the hoop SSH proxy.
const SSH_SUBTYPES = new Set(['ssh', 'ssh-local', 'git', 'github'])

// New sessions declare their byte format. Before the gateway persisted it, the
// viewer came from the resource type, and old recordings keep that choice: a
// subtype may have resolved to another protocol then. The ssh viewer replays
// a stream that is not SSH frames as a pty.
export function recordingFormat(session) {
  if (session.recording_format) return session.recording_format
  const { type, connection_subtype: subtype } = session
  if (type === 'custom' && subtype === 'rdp') return 'rdp'
  if (type === 'application' && SSH_SUBTYPES.has(subtype)) return 'ssh'
  if (['command-line', 'application', 'custom'].includes(type)) return 'pty'
  return 'raw'
}

// The output viewer of a session: 'exec', 'live', 'rdp', 'ssh', 'pty' or 'raw'.
// An exec keeps its viewer whatever it recorded. A connect session tails live
// while it is open; a finished postgres one keeps the tail, ended, because it
// decodes the queries. Other finished sessions replay their recording. An RDP
// session never tails: its events are bitmaps, not text (matrix P2).
export function outputViewer(session) {
  if (session.verb === 'exec') return 'exec'
  if (session.verb === 'connect' && recordingFormat(session) !== 'rdp') {
    const live = session.status === 'open'
    if (live || session.connection_subtype === 'postgres') return 'live'
  }
  return recordingFormat(session)
}
