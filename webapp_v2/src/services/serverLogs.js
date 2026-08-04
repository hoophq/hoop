import api from './api'
import { useAuthStore } from '@/stores/useAuthStore'

export const serverLogsService = {
  list: ({ limit = 500 } = {}) => api.get('/server-logs', { params: { limit } }),

  /**
   * Opens the server-logs SSE stream and invokes onEntry for every log event.
   * Implemented over fetch because EventSource cannot send the Authorization
   * header. Resolves when the stream ends; rejects on network/HTTP errors.
   * Cancel via an AbortController signal.
   */
  async stream({ backlog = 500, signal, onOpen, onEntry }) {
    const token = useAuthStore.getState().token
    const res = await fetch(`${api.defaults.baseURL}/server-logs/stream?backlog=${backlog}`, {
      headers: {
        Authorization: `Bearer ${token}`,
        Accept: 'text/event-stream',
      },
      signal,
    })
    if (!res.ok || !res.body) {
      throw new Error(`server-logs stream failed with status ${res.status}`)
    }
    onOpen?.()

    const reader = res.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    for (;;) {
      const { done, value } = await reader.read()
      if (done) return
      buffer += decoder.decode(value, { stream: true })
      let sep
      while ((sep = buffer.indexOf('\n\n')) >= 0) {
        const frame = buffer.slice(0, sep)
        buffer = buffer.slice(sep + 2)
        for (const line of frame.split('\n')) {
          if (!line.startsWith('data: ')) continue // skips ": connected" / ": keepalive" comments
          try {
            onEntry(JSON.parse(line.slice(6)))
          } catch {
            // skip malformed frame
          }
        }
      }
    }
  },
}
