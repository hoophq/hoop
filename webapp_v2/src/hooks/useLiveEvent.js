import { useEffect, useRef } from 'react'
import { liveEventsService } from '@/services/liveEvents'
import { useAuthStore } from '@/stores/useAuthStore'
import { useUserStore } from '@/stores/useUserStore'

// One stream per browser: a stream per tab would use up HTTP/1.1's 6
// connections to the gateway. The tab holding the lock streams for the others.
const listeners = new Map() // type -> Set of handlers
let controller = null
let channel = null

const LOCK = 'hoop-live-events'
const shared =
  typeof navigator !== 'undefined' && !!navigator.locks && typeof BroadcastChannel !== 'undefined'

// Spread, so every browser does not reconnect at once after a deploy.
const retryDelay = () => 3000 + Math.random() * 5000

const currentUserId = () => useUserStore.getState().user?.id ?? null

const sleep = (ms, signal) =>
  new Promise((resolve) => {
    const timer = setTimeout(resolve, ms)
    signal.addEventListener('abort', () => {
      clearTimeout(timer)
      resolve()
    }, { once: true })
  })

function subscriberCount() {
  let n = 0
  for (const set of listeners.values()) n += set.size
  return n
}

function dispatch(event) {
  for (const handler of listeners.get(event?.type) ?? []) handler(event)
}

function stream(signal) {
  const user = currentUserId()
  return liveEventsService.stream({
    signal,
    onEvent: (event) => {
      dispatch(event)
      channel?.postMessage({ event, user })
    },
  })
}

// The holder keeps the lock through the delay, so the next tab waits it too.
async function holdStream(signal) {
  await stream(signal).catch(() => {})
  if (!signal.aborted) await sleep(retryDelay(), signal)
}

function open() {
  if (controller) return
  const own = new AbortController()
  controller = own
  if (shared && !channel) {
    channel = new BroadcastChannel(LOCK)
    channel.onmessage = ({ data }) => {
      const me = currentUserId()
      if (data?.user && me && data.user !== me) return
      dispatch(data?.event)
    }
  }
  const run = shared
    ? navigator.locks.request(LOCK, { signal: own.signal }, () => holdStream(own.signal))
    : stream(own.signal)
  run
    .catch(() => {})
    .finally(() => {
      if (controller !== own) return
      controller = null
      // Ended by the server or the network while pages still listen: try
      // again. A page also keeps its own polling, so a lost stream only
      // costs speed.
      if (subscriberCount() === 0) return
      if (shared) open()
      else setTimeout(() => subscriberCount() > 0 && open(), retryDelay())
    })
}

function close() {
  controller?.abort()
  controller = null
  channel?.close()
  channel = null
}

// A stream opened with another user's token carries that user's events.
useAuthStore.subscribe((state, prev) => {
  if (state.token === prev.token || !controller) return
  close()
  if (subscriberCount() > 0) open()
})

/**
 * Calls handler with {type, id} for every live event of `type` while the
 * component is mounted (GET /events/stream). A notice names what changed,
 * never its content: read the resource again. Pass `enabled = false` to stop
 * listening without unmounting.
 */
export function useLiveEvent(type, handler, enabled = true) {
  const latest = useRef(handler)
  useEffect(() => {
    latest.current = handler
  }, [handler])

  useEffect(() => {
    if (!enabled) return undefined
    const call = (event) => latest.current(event)
    if (!listeners.has(type)) listeners.set(type, new Set())
    listeners.get(type).add(call)
    open()
    return () => {
      listeners.get(type)?.delete(call)
      if (listeners.get(type)?.size === 0) listeners.delete(type)
      if (subscriberCount() === 0) close()
    }
  }, [type, enabled])
}
