// The pure parts of the live tail: rows from the event stream, denied
// statements, the terminal text, the SSE frames, and times.
import { isQueryFrame, parsePayload } from './pgWire'
import { terminalRecording } from './sshDecoder'
import { b64ToText, decodeByteEvents, decodeEvents } from './terminalDecoder'

// Maps the stream time of each denied statement to the rules that denied there;
// two denials can share a time. Only entries with `elapsed` (sidecar sessions)
// point at a stream row.
export function deniedAt(guardrailsInfo) {
  const denied = new Map()
  for (const { elapsed, rule_name: rule } of guardrailsInfo || []) {
    if (typeof elapsed !== 'number') continue
    denied.set(elapsed, [...(denied.get(elapsed) || []), rule || ''])
  }
  return denied
}

// "Denied by rule a", "Denied by rules a, b", or "Denied by a guardrail".
export function deniedLabel(rules) {
  const named = [...new Set(rules.filter((rule) => rule.trim()))]
  if (!named.length) return 'Denied by a guardrail'
  return `Denied by ${named.length > 1 ? 'rules' : 'rule'} ${named.join(', ')}`
}

// Rows of one stream event. A postgres input chunk holds wire frames, one row
// each; a finished session read with event_stream=raw-queries holds plain SQL,
// one Query row.
function eventRows(postgres, index, [seconds, type, b64]) {
  if (postgres && type === 'i') {
    const frames = parsePayload(b64)
    if (frames.length) {
      return frames.map((frame, i) => ({
        key: `${index}-${i}`,
        seconds,
        type,
        pgType: frame.type,
        pgTypeName: frame.typeName,
        sql: frame.sql,
        kind: isQueryFrame(frame) ? 'query' : 'protocol',
      }))
    }
    return [{ key: `${index}`, seconds, type, pgTypeName: 'Query', sql: b64ToText(b64), kind: 'query' }]
  }
  const kind = type === 'o' ? 'output' : type === 'e' ? 'error' : 'raw'
  return [{ key: `${index}`, seconds, type, kind, text: b64ToText(b64) }]
}

// Rows of the stream, statements a guardrail denied flagged. The denial's own
// error row shares no time with its statement, so only statements match.
export function streamRows(eventStream, { postgres, denied }) {
  const rows = eventStream.flatMap((event, i) => eventRows(postgres, i, event))
  if (!denied?.size) return rows
  return rows.map((row) =>
    row.type !== 'e' && denied.has(row.seconds) ? { ...row, deniedRules: denied.get(row.seconds) } : row,
  )
}

// Decoded terminal events for the live view, or null to render event rows.
export function liveTerminalEvents(recordingFormat, eventStream, finalize) {
  const ssh = recordingFormat === 'ssh'
  const recording = ssh ? terminalRecording(eventStream) : null
  // Not SSH frames: recorded as a pty.
  if (recordingFormat === 'pty' || (ssh && eventStream.length && !recording)) {
    return decodeEvents(eventStream, finalize)
  }
  // No channel carried data yet: an interactive shell is the likely start, so
  // wait as a terminal. The events then hold only the gateway's errors.
  if (recording?.terminal || (ssh && (!eventStream.length || !recording.data))) {
    return decodeByteEvents(recording?.events || [], finalize)
  }
  return null
}

const pad = (n, width = 2) => String(n).padStart(width, '0')

// "+MM:SS.mmm" from the session start.
export function formatRelative(seconds) {
  const totalMs = Math.max(0, Math.floor(seconds * 1000))
  const totalS = Math.floor(totalMs / 1000)
  return `+${pad(Math.floor(totalS / 60))}:${pad(totalS % 60)}.${pad(totalMs % 1000, 3)}`
}

// The local time of an event, for the tooltip of its row.
export function formatAbsolute(startDate, seconds) {
  if (!startDate) return ''
  return new Date(new Date(startDate).getTime() + Math.floor(seconds * 1000)).toLocaleTimeString()
}

// "1h 2m", "3m 4s" or "5s" from start to end, or to now while the session runs.
export function durationBetween(startDate, endDate, now = Date.now()) {
  if (!startDate) return null
  const end = endDate ? new Date(endDate).getTime() : now
  const diff = Math.max(0, Math.floor((end - new Date(startDate).getTime()) / 1000))
  const h = Math.floor(diff / 3600)
  const m = Math.floor(diff / 60) % 60
  const s = diff % 60
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${s}s`
  return `${s}s`
}

// One SSE message block into { event, data }. Comments (": keepalive",
// ": connected") are skipped; several data lines join with a newline.
export function parseSseBlock(block) {
  let event = 'message'
  let data = null
  for (const line of block.split('\n')) {
    if (line.startsWith(':')) continue
    if (line.startsWith('event:')) event = line.slice(6).trim()
    else if (line.startsWith('data:')) {
      const chunk = line.slice(5).trim()
      data = data === null ? chunk : `${data}\n${chunk}`
    }
  }
  return { event, data }
}

// The complete messages in a buffer and what is left of it.
export function splitSse(buffer) {
  const messages = []
  let rest = buffer
  let at
  while ((at = rest.indexOf('\n\n')) >= 0) {
    const message = parseSseBlock(rest.slice(0, at))
    rest = rest.slice(at + 2)
    if (message.data !== null) messages.push(message)
  }
  return [messages, rest]
}

// A streamed event as a stream entry [seconds, type, payload]. A sidecar sends
// its stored row's elapsed time, which a denial points at; otherwise the time
// counts from the session start.
export function streamEntry(event, startDate) {
  let seconds = 0
  if (typeof event.elapsed === 'number') seconds = event.elapsed
  else if (startDate && event.time) {
    const start = new Date(startDate).getTime()
    const at = new Date(event.time).getTime()
    if (start > 0 && !Number.isNaN(at)) seconds = (at - start) / 1000
  }
  return [seconds, event.type, event.payload]
}

// The stored history counts seconds from its own clock and the stream from the
// event time, so one event can differ by a few milliseconds between the two.
const SAME_EVENT_SECONDS = 0.5

// The history read again after a (re)connect, joined with the events the
// stream already delivered, in time order. The stream sends nothing that
// happened before it subscribed; reading the history after the subscription
// fills that gap. A streamed event the history also holds counts once.
export function mergeStream(history, streamed) {
  const stored = new Map()
  for (const [seconds, type, payload] of history) {
    const key = `${type}|${payload}`
    stored.set(key, [...(stored.get(key) || []), seconds])
  }
  // Each stored event stands for at most one streamed one: a key pressed twice
  // within the tolerance still shows twice.
  const fresh = streamed.filter(([seconds, type, payload]) => {
    const times = stored.get(`${type}|${payload}`)
    const match = times?.findIndex((t) => Math.abs(t - seconds) < SAME_EVENT_SECONDS) ?? -1
    if (match < 0) return true
    times.splice(match, 1)
    return false
  })
  return [...history, ...fresh].sort((a, b) => a[0] - b[0])
}
