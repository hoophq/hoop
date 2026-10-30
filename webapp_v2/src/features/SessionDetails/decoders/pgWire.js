// The client side of the Postgres wire protocol, enough to show the SQL of a
// live session (a port of common/pgtypes/packet.go). One stream event carries
// one TCP chunk in base64; a chunk holds one or more frames, each a type byte,
// a uint32 big-endian length that counts itself, and a body.
import { b64ToBytes } from './terminalDecoder'

const TYPE_NAMES = {
  P: 'Parse',
  Q: 'Query',
  B: 'Bind',
  D: 'Describe',
  E: 'Execute',
  S: 'Sync',
  C: 'Close',
  H: 'Flush',
  X: 'Terminate',
  F: 'FunctionCall',
  p: 'Password',
}

export const typeName = (type) => TYPE_NAMES[type] || type

const readUint32 = (bytes, offset) =>
  ((bytes[offset] << 24) | (bytes[offset + 1] << 16) | (bytes[offset + 2] << 8) | bytes[offset + 3]) >>> 0

const utf8 = (bytes, start, end) => new TextDecoder('utf-8').decode(bytes.subarray(start, end))

// One frame at offset, or null when it is truncated.
function parseFrame(bytes, offset) {
  if (bytes.length - offset < 5) return null
  const type = String.fromCharCode(bytes[offset])
  const length = readUint32(bytes, offset + 1)
  const end = offset + 1 + length
  if (length <= 0 || end > bytes.length) return null
  const bodyStart = offset + 5
  let sql = null
  if (type === 'Q' && end > bodyStart) {
    // Simple query: the SQL ends with a NUL.
    sql = utf8(bytes, bodyStart, end - 1)
  } else if (type === 'P') {
    // Parse: statement name, NUL, SQL, NUL, parameter types.
    const nameEnd = bytes.indexOf(0, bodyStart)
    if (nameEnd >= 0 && nameEnd < end) {
      const sqlEnd = bytes.indexOf(0, nameEnd + 1)
      if (sqlEnd >= 0 && sqlEnd < end) sql = utf8(bytes, nameEnd + 1, sqlEnd)
    }
  }
  return { type, typeName: typeName(type), sql, next: end }
}

// The frames of one base64 chunk: [{ type, typeName, sql }]. sql is set only
// for Parse and Query. A truncated or broken tail is dropped: a client may
// split a message across TCP segments.
export function parsePayload(b64) {
  let bytes
  try {
    bytes = b64ToBytes(b64 || '')
  } catch {
    return []
  }
  const frames = []
  let offset = 0
  while (offset < bytes.length) {
    const frame = parseFrame(bytes, offset)
    if (!frame) break
    frames.push({ type: frame.type, typeName: frame.typeName, sql: frame.sql })
    offset = frame.next
  }
  return frames
}

// A frame that carries SQL text.
export const isQueryFrame = (frame) => typeof frame?.sql === 'string' && frame.sql.length > 0
