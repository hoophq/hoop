/**
 * A single 1 Hz clock shared by every countdown in the app.
 *
 * Reference counted: the interval starts with the first subscriber and stops
 * with the last, so N session rows cost one timer instead of N, and an app with
 * no live countdown costs nothing.
 */
let now = Date.now()
let intervalId = null
const subscribers = new Set()

export function subscribeTick(callback) {
  subscribers.add(callback)
  if (intervalId === null) {
    intervalId = setInterval(() => {
      now = Date.now()
      subscribers.forEach((fn) => fn())
    }, 1000)
  }
  return () => {
    subscribers.delete(callback)
    if (subscribers.size === 0 && intervalId !== null) {
      clearInterval(intervalId)
      intervalId = null
    }
  }
}

// Returns the CACHED timestamp, never a fresh Date.now(). useSyncExternalStore
// requires a snapshot that is stable between notifications — returning a new
// value on every call would loop forever.
export function getTick() {
  return now
}
