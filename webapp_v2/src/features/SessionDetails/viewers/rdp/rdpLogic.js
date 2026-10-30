// Pure rules of the RDP replay and its PII analysis (matrix P01-P11, I12, I13).

// Statuses that keep the analysis poll running; done and failed end it.
export const ACTIVE_ANALYSIS_STATUSES = new Set(['pending', 'running', 'analyzing'])

// The gateway reports 'running' for a job in flight; 'analyzing' is the older
// value kept in sessions.metrics. 'not_analyzed' is a session recorded before
// the analysis existed: it can still run on demand.
const STATUS_PRESENTATION = {
  pending: { label: 'Queued', color: 'gray' },
  running: { label: 'Analyzing PII', color: 'blue' },
  analyzing: { label: 'Analyzing PII', color: 'blue' },
  done: { label: 'Analysis ready', color: 'green' },
  failed: { label: 'Analysis failed', color: 'red' },
  not_analyzed: { label: 'Not analyzed', color: 'gray' },
}

// The gateway's `analysis` object, or the older `analysis_status` string.
export function normalizeAnalysis(response) {
  const info = response?.analysis || {}
  return {
    status: info.status || response?.analysis_status || '',
    attempt: info.attempt || 0,
    maxAttempts: info.max_attempts || 0,
    lastError: info.last_error || '',
    totalDetections: (response?.detections || []).length,
  }
}

// What the status row shows, or null when there is no status (the analysis is
// off, or the session is not an RDP recording).
export function analysisBadges(analysis) {
  const { status, attempt, maxAttempts, lastError, totalDetections } = analysis
  if (!status) return null
  const failed = status === 'failed'
  return {
    ...(STATUS_PRESENTATION[status] || { label: `Status: ${status}`, color: 'gray' }),
    attempt: status === 'running' && attempt > 0 && maxAttempts > 1 ? `Attempt ${attempt}/${maxAttempts}` : null,
    gaveUp: failed && attempt > 0 && attempt >= maxAttempts ? `Gave up after ${maxAttempts} attempts` : null,
    error: failed && lastError ? lastError : null,
    // A job that failed for good, or one that never ran, can still start.
    retry: failed ? 'Retry analysis' : status === 'not_analyzed' ? 'Run analysis' : null,
    noEntities: status === 'done' && totalDetections === 0,
  }
}

// Detections grouped by snapshot time, oldest first: [{ timestamp, detections }].
export function groupDetectionsBySnapshot(detections = []) {
  const byTime = new Map()
  for (const detection of detections) {
    const list = byTime.get(detection.timestamp)
    if (list) list.push(detection)
    else byTime.set(detection.timestamp, [detection])
  }
  return [...byTime.entries()]
    .sort(([a], [b]) => a - b)
    .map(([timestamp, list]) => ({ timestamp, detections: list }))
}

// The detections of the latest snapshot taken at or before `time`, or null.
export function activeDetections(snapshots, time) {
  let lo = 0
  let hi = snapshots.length - 1
  let best = null
  while (lo <= hi) {
    const mid = (lo + hi) >> 1
    if (snapshots[mid].timestamp <= time) {
      best = snapshots[mid].detections
      lo = mid + 1
    } else {
      hi = mid - 1
    }
  }
  return best
}

export function entityTypesOf(detections = []) {
  return [...new Set(detections.map((d) => d.entity_type).filter(Boolean))].sort()
}

// Types the user turned off stay off when the poll brings new detections; types
// seen for the first time start on.
export function mergeEnabledTypes(enabled, previousTypes, nextTypes) {
  const previous = new Set(previousTypes)
  return new Set(nextTypes.filter((type) => enabled.has(type) || !previous.has(type)))
}

// The last frame drawn at or before `time`. Frames are sorted by timestamp.
export function frameIndexAt(frames, time) {
  let lo = 0
  let hi = frames.length - 1
  let best = 0
  while (lo <= hi) {
    const mid = (lo + hi) >> 1
    if (frames[mid].timestamp <= time) {
      best = mid
      lo = mid + 1
    } else {
      hi = mid - 1
    }
  }
  return best
}

// Ask for the next page while playback is within this many frames of the end
// of what is loaded.
export const PREFETCH_MARGIN = 10

export function needsMoreFrames(index, loaded, total) {
  return loaded < total && index >= loaded - PREFETCH_MARGIN
}

// "01:05" for 65 seconds.
export function formatClock(seconds) {
  const safe = Math.max(0, seconds || 0)
  const pad = (n) => String(Math.floor(n)).padStart(2, '0')
  return `${pad(safe / 60)}:${pad(safe % 60)}`
}

export function decodeBase64(b64) {
  try {
    const binary = atob(b64)
    const bytes = new Uint8Array(binary.length)
    for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i)
    return bytes
  } catch {
    return null
  }
}

// The canvas the recording was drawn on. Sessions recorded without it get the
// gateway's default size.
export function recordingSize(metrics = {}) {
  return {
    width: metrics.canvas_width || 1280,
    height: metrics.canvas_height || 720,
  }
}

// Each entity type has one theme color, in the legend and on the overlay.
const ENTITY_COLORS = {
  PERSON: 'red',
  EMAIL_ADDRESS: 'orange',
  PHONE_NUMBER: 'amber',
  URL: 'blue',
  LOCATION: 'green',
  DATE_TIME: 'violet',
  NRP: 'sky',
}

export const entityColor = (type) => ENTITY_COLORS[type] || 'gray'
