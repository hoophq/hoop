import api, { storeRenewedToken } from './api'
import { useAuthStore } from '@/stores/useAuthStore'

// The event types the gateway sends (gateway/liveevents).
export const LIVE_EVENTS = {
  SIDECAR_CHECKED_IN: 'sidecar.checked_in',
  REVIEW_CREATED: 'review.created',
}

export const liveEventsService = {
  /**
   * Opens the gateway's live events SSE stream (GET /events/stream) and calls
   * onEvent with {type, id} for every change notice. Over fetch, because
   * EventSource cannot send the Authorization header. Resolves when the
   * stream ends; rejects on network/HTTP errors. Cancel via an AbortController
   * signal.
   */
  async stream({ signal, onEvent }) {
    const token = useAuthStore.getState().getToken()
    if (!token) throw new Error('live events stream: no token')
    const res = await fetch(`${api.defaults.baseURL}/events/stream`, {
      headers: { Authorization: `Bearer ${token}`, Accept: 'text/event-stream' },
      signal,
    })
    storeRenewedToken(token, res.headers)
    if (!res.ok || !res.body) {
      throw new Error(`live events stream failed with status ${res.status}`)
    }
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
            onEvent(JSON.parse(line.slice(6)))
          } catch {
            // skip malformed frame
          }
        }
      }
    }
  },
}
