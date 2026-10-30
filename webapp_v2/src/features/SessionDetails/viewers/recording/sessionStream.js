import { splitSse } from '../../decoders/liveTail'

// The stream answered with an HTTP error: the session is gone or not visible.
export class StreamRefused extends Error {}
// The stream closed without session_end: a proxy or the network dropped it.
export class StreamDropped extends Error {}

// Reads GET /sessions/:id/stream over fetch: EventSource cannot send the
// Authorization header. Calls onResponse with the response headers, on an error
// too (a renewed token, services/api.js), onOpen once the gateway answers, then
// onMessage with each { event, data }. Resolves on session_end; throws
// StreamRefused on an HTTP error and StreamDropped when the stream closes
// before session_end.
export async function readSessionStream(url, { token, signal, onResponse, onOpen, onMessage }) {
  const response = await fetch(url, {
    headers: { Authorization: `Bearer ${token}`, Accept: 'text/event-stream' },
    cache: 'no-store',
    signal,
  })
  onResponse?.(response.headers)
  if (!response.ok || !response.body) throw new StreamRefused(`stream answered ${response.status}`)
  onOpen()
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  for (;;) {
    const { done, value } = await reader.read()
    if (done) throw new StreamDropped('stream closed before session_end')
    buffer += decoder.decode(value, { stream: true })
    const [messages, rest] = splitSse(buffer)
    buffer = rest
    for (const message of messages) {
      if (message.event === 'session_end') {
        reader.cancel().catch(() => {})
        return
      }
      onMessage(message)
    }
  }
}
