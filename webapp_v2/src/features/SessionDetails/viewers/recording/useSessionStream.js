import { useEffect, useMemo, useRef, useState } from 'react'
import api from '@/services/api'
import { sessionsService } from '@/services/sessions'
import { useAuthStore } from '@/stores/useAuthStore'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'
import { mergeStream, streamEntry } from '../../decoders/liveTail'
import { readSessionStream, StreamRefused } from './sessionStream'

// A sidecar denial writes an "e" row; its guardrails_info entry reaches the
// session a moment later. One read covers a burst.
const GUARDRAILS_REFRESH_MS = 1500
// A dropped stream reconnects after 1, 2, 4, 8 and 16 seconds, then gives up.
const RECONNECT_DELAYS_MS = [1000, 2000, 4000, 8000, 16000]

const sleep = (ms, signal) =>
  new Promise((resolve) => {
    const timer = setTimeout(resolve, ms)
    signal.addEventListener('abort', () => {
      clearTimeout(timer)
      resolve()
    })
  })

// Follows GET /sessions/:id/stream while the session is open. Returns the
// stream state ('connecting' | 'live' | 'ended' | 'error') and the events: the
// stored history, read again after each (re)connect because the stream sends
// no catch-up, joined with what the stream delivered.
export function useSessionStream(session, { enabled, catchUp }) {
  const history = session.event_stream
  const [streamed, setStreamed] = useState({ id: session.id, events: [], stored: null })
  const [state, setState] = useState({ id: session.id, value: 'connecting' })
  const eventsRef = useRef([])

  useEffect(() => {
    if (!enabled) return undefined
    const controller = new AbortController()
    const { signal } = controller
    const sessionId = session.id
    const startDate = session.start_date
    const sidecar = session.identity_type === 'sidecar'
    let stored = null
    let refreshPending = false
    eventsRef.current = []
    const setValue = (value) => setState({ id: sessionId, value })

    const refreshGuardrails = async () => {
      refreshPending = false
      try {
        const { data } = await sessionsService.get(sessionId)
        if (!signal.aborted) useSessionDetailsStore.getState().patchSession({ guardrails_info: data.guardrails_info })
      } catch {
        // The next denial reads it again.
      }
    }

    const readHistory = async () => {
      if (!catchUp) return
      try {
        const { data } = await sessionsService.get(sessionId, { expand: 'event_stream' })
        if (signal.aborted || !Array.isArray(data?.event_stream)) return
        stored = data.event_stream
        setStreamed({ id: sessionId, events: eventsRef.current, stored })
      } catch {
        // The history already shown stays.
      }
    }

    const end = () => {
      setValue('ended')
      const events = mergeStream(stored || history || [], eventsRef.current)
      useSessionDetailsStore.getState().patchSession({
        status: 'done',
        end_date: new Date().toISOString(),
        event_stream: events,
      })
    }

    const onMessage = ({ event, data }) => {
      if (event !== 'event') return
      let parsed
      try {
        parsed = JSON.parse(data)
      } catch {
        return
      }
      eventsRef.current = [...eventsRef.current, streamEntry(parsed, startDate)]
      setStreamed({ id: sessionId, events: eventsRef.current, stored })
      if (parsed.type === 'e' && sidecar && !refreshPending) {
        refreshPending = true
        setTimeout(refreshGuardrails, GUARDRAILS_REFRESH_MS)
      }
    }

    // A dropped stream reconnects: the gateway answers session_end at once
    // when the session ended meanwhile, and the history fills the gap.
    const run = async () => {
      let failures = 0
      while (!signal.aborted) {
        setValue('connecting')
        try {
          await readSessionStream(`${api.defaults.baseURL}/sessions/${encodeURIComponent(sessionId)}/stream`, {
            token: useAuthStore.getState().token,
            signal,
            onOpen: () => {
              setValue('live')
              failures = 0
              readHistory()
            },
            onMessage,
          })
          if (!signal.aborted) end()
          return
        } catch (err) {
          if (signal.aborted) return
          if (err instanceof StreamRefused || failures >= RECONNECT_DELAYS_MS.length) {
            setValue('error')
            return
          }
          await sleep(RECONNECT_DELAYS_MS[failures++], signal)
        }
      }
    }
    run()
    return () => controller.abort()
    // The stream follows one session; history and the flags are read at start.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [session.id, enabled])

  const own = streamed.id === session.id
  const events = useMemo(
    () => (own ? mergeStream(streamed.stored || history || [], streamed.events) : history || []),
    [own, streamed, history],
  )
  let value = state.id === session.id ? state.value : 'connecting'
  // Reopening a session that already ended shows it ended.
  if (!enabled) value = 'ended'
  return { state: value, events }
}
