// Decodes terminal recordings. Each audit event is [seconds, type, payload]:
// type 'o' (output) and 'e' (error) share one byte stream, 'i' (input) is a
// stream of its own, and 'r' (resize) carries "COLSxROWS" text.

export function b64ToBytes(b64) {
  const binary = atob(b64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i)
  return bytes
}

// The UTF-8 text of a base64 payload. Bytes that are not UTF-8 show as U+FFFD;
// a payload that is not base64 shows as no text.
export function b64ToText(b64) {
  try {
    return new TextDecoder('utf-8').decode(b64ToBytes(b64 || ''))
  } catch {
    return ''
  }
}

// Decode an ordered stream of [seconds, type, Uint8Array] events. Input must not
// complete an output code point. While the stream is live (finalize false) an
// unfinished code point stays buffered; at the end it shows as U+FFFD.
export function decodeByteEvents(events, finalize = true) {
  const outputDecoder = new TextDecoder('utf-8')
  const inputDecoder = new TextDecoder('utf-8')
  const decoded = []
  let lastOutput = null
  let lastInput = null
  for (const event of events) {
    const [seconds, type, data] = event
    if (type === 'r') {
      decoded.push(event)
      continue
    }
    const output = type === 'o' || type === 'e'
    const text = (output ? outputDecoder : inputDecoder).decode(data, { stream: true })
    if (output) lastOutput = decoded.length
    else lastInput = decoded.length
    decoded.push([seconds, type, text])
  }
  if (finalize) {
    const outputEnd = outputDecoder.decode()
    const inputEnd = inputDecoder.decode()
    if (lastOutput !== null && outputEnd) decoded[lastOutput][2] += outputEnd
    if (lastInput !== null && inputEnd) decoded[lastInput][2] += inputEnd
  }
  return decoded
}

// decodeByteEvents over an audit stream of [seconds, type, base64] events.
export function decodeEvents(eventStream, finalize = true) {
  return decodeByteEvents(
    eventStream.map(([seconds, type, b64]) => [seconds, type, b64ToBytes(b64)]),
    finalize,
  )
}

// The text a terminal shows: output and errors only. A pty echoes the input
// back as output, so the input would show every keystroke twice.
export function terminalText(decodedEvents) {
  return decodedEvents
    .filter(([, type]) => type === 'o' || type === 'e')
    .map(([, , text]) => text)
    .join('')
}
