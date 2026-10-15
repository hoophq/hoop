import { useEffect, useRef } from 'react'
import { liveEventsService } from '@/services/liveEvents'

// One stream for the whole tab, shared by every subscriber. It opens with the
// first subscriber and closes with the last, so a page that leaves stops
// listening and a tab with no listening page holds no connection.
const listeners = new Map() // type -> Set of handlers
let controller = null

const RETRY_MS = 5000

function subscriberCount() {
  let n = 0
  for (const set of listeners.values()) n += set.size
  return n
}

function dispatch(event) {
  for (const handler of listeners.get(event?.type) ?? []) handler(event)
}

function open() {
  if (controller) return
  const own = new AbortController()
  controller = own
  liveEventsService
    .stream({ signal: own.signal, onEvent: dispatch })
    .catch(() => {})
    .finally(() => {
      if (controller !== own) return
      controller = null
      // Ended by the server or the network while pages still listen: try
      // again. A page also keeps its own polling, so a lost stream only
      // costs speed.
      if (subscriberCount() > 0) setTimeout(() => subscriberCount() > 0 && open(), RETRY_MS)
    })
}

function close() {
  controller?.abort()
  controller = null
}

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
