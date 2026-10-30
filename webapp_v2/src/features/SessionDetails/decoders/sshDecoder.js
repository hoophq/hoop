// Reads hoop SSH recordings (libhoop proxy/ssh/types). Each audit event holds
// one frame: a type byte, the channel id (uint16, little endian), then the
// payload. Input events come from the client, output events from the server.
//
// Channel ids restart at 1 on each SSH connection of a session, and the audit
// stream does not tell connections apart. Sequential connections are fine:
// each channel closes before its id opens again. An id that opens while it is
// still open means two connections overlap, and their bytes cannot be told
// apart; such a recording is not replayed as a terminal.
//
// A live stream can start after a channel was opened. Nothing in the stream
// then tells whether that channel is a terminal, so its bytes are not replayed
// as one; it still counts as open for the overlap check.
import { b64ToBytes } from './terminalDecoder'

const OPEN_CHANNEL = 1
const CHANNEL_REQUEST = 2
const CHANNEL_DATA = 3
const CLOSE_CHANNEL = 4
const LAST_FRAME_TYPE = 7

const DEFAULT_SIZE = { width: 80, height: 24 }

function frameOf(data) {
  if (data.length < 3) return null
  const type = data[0]
  if (type < OPEN_CHANNEL || type > LAST_FRAME_TYPE) return null
  return { type, channel: data[1] | (data[2] << 8) }
}

// The text from offset to the next NUL and the offset after it, or null when
// no NUL follows.
function nulTerminated(data, offset) {
  const end = data.indexOf(0, offset)
  if (end < 0) return null
  return [String.fromCharCode(...data.subarray(offset, end)), end + 1]
}

const uint32 = (data, offset) =>
  new DataView(data.buffer, data.byteOffset, data.byteLength).getUint32(offset, false)

const terminalSize = (width, height) => (width > 0 && height > 0 ? { width, height } : null)

// Columns and rows of a pty-req payload (RFC 4254 6.2): string TERM, then
// uint32 columns and rows.
function ptyReqSize(payload) {
  if (payload.length < 4) return null
  const colsAt = 4 + uint32(payload, 0)
  if (colsAt + 8 > payload.length) return null
  return terminalSize(uint32(payload, colsAt), uint32(payload, colsAt + 4))
}

// Columns and rows of a window-change payload (RFC 4254 6.7).
function windowChangeSize(payload) {
  if (payload.length < 8) return null
  return terminalSize(uint32(payload, 0), uint32(payload, 4))
}

// Remember a channel that sends frames without an open frame in the stream, so
// that an open of the same id counts as an overlap until it closes.
function noteUnopened(state, channel) {
  if (!state.channels.has(channel) && !state.closed.has(channel)) state.unopened.add(channel)
}

// pty-req makes a session channel a terminal; pty-req and window-change set the
// terminal size. Two terminals open at once cannot be replayed as one.
function onRequest(state, seconds, channel, request, payload) {
  const info = state.channels.get(channel) || {}
  const ptyReq = info.session && request === 'pty-req'
  let size = null
  if (ptyReq) size = ptyReqSize(payload) || DEFAULT_SIZE
  else if (info.pty && request === 'window-change') size = windowChangeSize(payload)
  noteUnopened(state, channel)
  if (ptyReq) {
    const otherTerminal = [...state.channels].some(([id, other]) => id !== channel && other.pty)
    state.channels.set(channel, { ...info, pty: true })
    if (otherTerminal) state.overlapping = true
  }
  if (size && state.size) state.events.push([seconds, 'r', `${size.width}x${size.height}`])
  if (size && !state.size) state.size = size
}

// Applies one frame to the state. Returns false when the bytes are not a frame.
function onFrame(state, seconds, type, data, frame) {
  const { channel } = frame
  switch (frame.type) {
    case OPEN_CHANNEL: {
      const parsed = nulTerminated(data, 3)
      if (!parsed) return false
      if (state.channels.has(channel) || state.unopened.has(channel)) state.overlapping = true
      state.channels.set(channel, { session: parsed[0] === 'session' })
      state.closed.delete(channel)
      return true
    }
    case CHANNEL_REQUEST: {
      const parsed = nulTerminated(data, 3)
      if (!parsed) return false
      // One want-reply byte follows the request name.
      onRequest(state, seconds, channel, parsed[0], data.subarray(parsed[1] + 1))
      return true
    }
    case CHANNEL_DATA:
      noteUnopened(state, channel)
      state.data = true
      if (state.channels.get(channel)?.pty) state.events.push([seconds, type, data.subarray(3)])
      return true
    case CLOSE_CHANNEL:
      state.channels.delete(channel)
      state.unopened.delete(channel)
      state.closed.add(channel)
      return true
    default:
      noteUnopened(state, channel)
      return true
  }
}

// Reads an audit stream of [seconds, type, base64] events as an SSH recording.
// Returns null when the stream is not SSH frames: a session of a subtype that
// was a pty connection when it was recorded. Otherwise returns
// { terminal, data, width, height, events }: terminal when a channel is a
// terminal and no two terminals or connections overlap; data once any channel
// carried data; events are [seconds, type, Uint8Array] for the data of
// terminal channels and the gateway's errors, and [seconds, 'r', 'COLSxROWS']
// for size changes.
export function terminalRecording(eventStream) {
  const state = {
    channels: new Map(),
    unopened: new Set(),
    closed: new Set(),
    events: [],
    size: null,
    framed: false,
    overlapping: false,
    data: false,
  }
  for (const [seconds, type, b64] of eventStream) {
    const data = b64ToBytes(b64)
    if (data.length === 0) continue
    // The gateway writes its own error text when the session ends.
    if (type === 'e') {
      state.events.push([seconds, type, data])
      continue
    }
    const frame = frameOf(data)
    if (!frame || !onFrame(state, seconds, type, data, frame)) return null
    state.framed = true
  }
  if (!state.framed) return null
  return {
    ...(state.size || DEFAULT_SIZE),
    // Every pty-req sets the size.
    terminal: Boolean(state.size) && !state.overlapping,
    data: state.data,
    events: state.events,
  }
}
