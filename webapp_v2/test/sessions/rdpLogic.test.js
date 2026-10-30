import { describe, expect, it } from 'vitest'
import {
  activeDetections,
  analysisBadges,
  decodeBase64,
  entityTypesOf,
  formatClock,
  frameIndexAt,
  groupDetectionsBySnapshot,
  mergeEnabledTypes,
  needsMoreFrames,
  normalizeAnalysis,
  recordingSize,
} from '@/features/SessionDetails/viewers/rdp/rdpLogic'

const frames = [0.5, 0.5, 1, 2.5, 4].map((timestamp, index) => ({ index, timestamp }))

describe('frameIndexAt', () => {
  it.each([
    [0, 0],
    [0.5, 1],
    [0.9, 1],
    [1, 2],
    [3, 3],
    [99, 4],
  ])('at %s is frame %s', (time, expected) => {
    expect(frameIndexAt(frames, time)).toBe(expected)
  })

  it('is 0 without frames', () => {
    expect(frameIndexAt([], 3)).toBe(0)
  })
})

describe('needsMoreFrames', () => {
  it('asks within 10 frames of the end of what is loaded', () => {
    expect(needsMoreFrames(89, 100, 300)).toBe(false)
    expect(needsMoreFrames(90, 100, 300)).toBe(true)
  })

  it('stops when everything is loaded', () => {
    expect(needsMoreFrames(299, 300, 300)).toBe(false)
  })
})

describe('detections', () => {
  const detections = [
    { timestamp: 4.5, entity_type: 'EMAIL_ADDRESS' },
    { timestamp: 3.5, entity_type: 'PERSON' },
    { timestamp: 3.5, entity_type: 'URL' },
  ]
  const snapshots = groupDetectionsBySnapshot(detections)

  it('groups by snapshot, oldest first', () => {
    expect(snapshots.map((s) => [s.timestamp, s.detections.length])).toEqual([
      [3.5, 2],
      [4.5, 1],
    ])
  })

  it('shows the latest snapshot at or before the time', () => {
    expect(activeDetections(snapshots, 3)).toBeNull()
    expect(activeDetections(snapshots, 3.5)).toHaveLength(2)
    expect(activeDetections(snapshots, 4)).toHaveLength(2)
    expect(activeDetections(snapshots, 10)).toEqual([detections[0]])
  })

  it('lists each entity type once, sorted', () => {
    expect(entityTypesOf(detections)).toEqual(['EMAIL_ADDRESS', 'PERSON', 'URL'])
  })

  it('keeps a type the user turned off, and turns on a new one', () => {
    const enabled = new Set(['PERSON'])
    const next = mergeEnabledTypes(enabled, ['PERSON', 'URL'], ['PERSON', 'URL', 'EMAIL_ADDRESS'])
    expect([...next].sort()).toEqual(['EMAIL_ADDRESS', 'PERSON'])
  })
})

describe('analysis', () => {
  it('reads the analysis object', () => {
    expect(
      normalizeAnalysis({
        analysis: { status: 'failed', attempt: 3, max_attempts: 3, last_error: 'refused' },
        detections: [],
      }),
    ).toEqual({ status: 'failed', attempt: 3, maxAttempts: 3, lastError: 'refused', totalDetections: 0 })
  })

  it('falls back to analysis_status from older gateways', () => {
    expect(normalizeAnalysis({ analysis_status: 'running' }).status).toBe('running')
  })

  it('shows nothing without a status', () => {
    expect(analysisBadges(normalizeAnalysis({ analysis: { status: '' } }))).toBeNull()
  })

  it.each([
    ['pending', { label: 'Queued', retry: null }],
    ['running', { label: 'Analyzing PII', retry: null }],
    ['analyzing', { label: 'Analyzing PII', retry: null }],
    ['done', { label: 'Analysis ready', retry: null }],
    ['failed', { label: 'Analysis failed', retry: 'Retry analysis' }],
    ['not_analyzed', { label: 'Not analyzed', retry: 'Run analysis' }],
    ['weird', { label: 'Status: weird', retry: null }],
  ])('%s', (status, expected) => {
    expect(analysisBadges({ status, attempt: 0, maxAttempts: 0, lastError: '', totalDetections: 1 })).toMatchObject(
      expected,
    )
  })

  it('counts attempts while running', () => {
    const badges = analysisBadges({ status: 'running', attempt: 2, maxAttempts: 3, lastError: '', totalDetections: 0 })
    expect(badges.attempt).toBe('Attempt 2/3')
  })

  it('says it gave up and why', () => {
    const badges = analysisBadges({
      status: 'failed',
      attempt: 3,
      maxAttempts: 3,
      lastError: 'presidio analyzer: connection refused',
      totalDetections: 0,
    })
    expect(badges.gaveUp).toBe('Gave up after 3 attempts')
    expect(badges.error).toBe('presidio analyzer: connection refused')
  })

  it('says when a done analysis found nothing', () => {
    expect(analysisBadges({ status: 'done', attempt: 1, maxAttempts: 3, lastError: '', totalDetections: 0 }).noEntities).toBe(
      true,
    )
  })
})

describe('helpers', () => {
  it('formats the clock', () => {
    expect(formatClock(0)).toBe('00:00')
    expect(formatClock(65.9)).toBe('01:05')
    expect(formatClock(9.5)).toBe('00:09')
  })

  it('decodes base64, or gives null', () => {
    expect([...decodeBase64('AQID')]).toEqual([1, 2, 3])
    expect(decodeBase64('%%%')).toBeNull()
  })

  it('falls back to 1280x720', () => {
    expect(recordingSize({})).toEqual({ width: 1280, height: 720 })
    expect(recordingSize({ canvas_width: 800, canvas_height: 480 })).toEqual({ width: 800, height: 480 })
  })
})
