import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'

dayjs.extend(relativeTime)

export function timeAgo(date) {
  return date ? dayjs(date).fromNow() : '—'
}

export function formatDateTime(date) {
  return date ? dayjs(date).format('MMM D, YYYY HH:mm') : '—'
}

export function startOfTodayISO() {
  return dayjs().startOf('day').toISOString()
}

// Go time.Duration is serialized as nanoseconds (e.g. review access_duration).
export function formatDurationNs(ns) {
  if (!ns) return null
  const totalMinutes = Math.round(ns / 60_000_000_000)
  const hours = Math.floor(totalMinutes / 60)
  const minutes = totalMinutes % 60
  if (hours && minutes) return `${hours}h ${minutes}m`
  if (hours) return `${hours}h`
  return `${minutes}m`
}
