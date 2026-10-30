export const JOBS_PATH = '/integrations/aws-connect'
export const SETUP_PATH = '/integrations/aws-connect/setup'
export const POLL_INTERVAL_MS = 5000

export function errorMessage(err, fallback) {
  const data = err?.response?.data
  if (typeof data === 'string' && data.trim()) return data
  return data?.message || fallback
}

export function isRunning(job) {
  return !job.completed_at
}

// The free plan imports one resource: the gateway refuses a job after one that did not fail.
export function limitReached(jobs) {
  return jobs.some((job) => job.status?.phase !== 'failed')
}

function lastSegment(value, separator) {
  return (value || '').split(separator).pop()
}

function joinName(...parts) {
  return parts.filter(Boolean).join('-')
}

export function toJobRows(jobs) {
  return jobs
    .map((job) => {
      const name = lastSegment(job.spec?.db_arn, ':')
      const phase = job.status?.phase ?? ''
      const children = (job.status?.result ?? []).map((item, index) => ({
        id: `${job.id}-${item.user_role || index}`,
        name: joinName(name, lastSegment(item.user_role, '_')),
        status: item.status ?? '',
        createdAt: null,
        completedAt: item.completed_at ?? null,
        error: item.status === 'failed' ? item.message || 'Failed' : null,
      }))
      return {
        id: job.id,
        name,
        status: phase,
        createdAt: job.created_at ?? null,
        completedAt: job.completed_at ?? null,
        error: phase === 'failed' ? job.status?.message || 'Failed' : null,
        children,
      }
    })
    .sort((a, b) => (b.createdAt ?? '').localeCompare(a.createdAt ?? ''))
}

export function formatDate(value) {
  if (!value) return ''
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
