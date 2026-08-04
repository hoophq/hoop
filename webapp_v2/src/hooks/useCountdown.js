import { useSyncExternalStore } from 'react'
import { subscribeTick, getTick } from '@/utils/tick'

const padZero = (n) => String(n).padStart(2, '0')

/**
 * Formats a remaining duration the way the legacy components/timer.cljs did:
 * HH:MM:SS above an hour, MM:SS below it, always zero-padded.
 */
export function formatRemaining(ms) {
  const totalSeconds = Math.floor(ms / 1000)
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor(totalSeconds / 60) % 60
  const seconds = totalSeconds % 60
  return hours > 0
    ? `${padZero(hours)}:${padZero(minutes)}:${padZero(seconds)}`
    : `${padZero(minutes)}:${padZero(seconds)}`
}

/**
 * Display-only countdown. Expiry is NOT handled here — useNativeAccessStore owns
 * it, so it fires exactly once per session regardless of how many components
 * render the same expire_at, and it still fires when nothing is rendered at all.
 * (The CLJS version attached the completion callback to every mounted timer,
 * which double-fired the "session has expired" toast whenever the modal and the
 * draggable card were open together.)
 *
 * `expireAt` of null means a persistent credential — no countdown.
 */
export default function useCountdown(expireAt) {
  const now = useSyncExternalStore(subscribeTick, getTick, getTick)
  if (!expireAt) return { remainingMs: null, label: null, expired: false }
  const remainingMs = Math.max(0, new Date(expireAt).getTime() - now)
  return { remainingMs, label: formatRemaining(remainingMs), expired: remainingMs <= 0 }
}
