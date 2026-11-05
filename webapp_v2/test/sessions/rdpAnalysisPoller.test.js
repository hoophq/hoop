import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ANALYSIS_POLL_MS, createAnalysisPoller } from '@/features/SessionDetails/viewers/rdp/analysisPoller'

const payload = (status, detections = []) => ({ data: { detections, analysis: { status, attempt: 1, max_attempts: 3 } } })
const httpError = (status) => Object.assign(new Error(`HTTP ${status}`), { response: { status } })

function setup(responses, extra = {}) {
  const fetch = vi.fn()
  for (const r of responses) {
    if (r instanceof Error) fetch.mockRejectedValueOnce(r)
    else fetch.mockResolvedValueOnce(r)
  }
  const onDone = vi.fn()
  const poller = createAnalysisPoller({ fetch, retry: vi.fn(), onDone, ...extra })
  return { fetch, onDone, poller }
}

describe('analysis poller', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('polls every 5 s while the job runs, and stops when it is done', async () => {
    const { fetch, onDone, poller } = setup([payload('pending'), payload('running'), payload('done', [{}])])
    poller.hold('s1')
    await vi.advanceTimersByTimeAsync(0)
    expect(poller.snapshot('s1').analysis.status).toBe('pending')
    await vi.advanceTimersByTimeAsync(ANALYSIS_POLL_MS)
    await vi.advanceTimersByTimeAsync(ANALYSIS_POLL_MS)
    expect(fetch).toHaveBeenCalledTimes(3)
    expect(poller.snapshot('s1').analysis.status).toBe('done')
    expect(onDone).toHaveBeenCalledWith('s1')
    await vi.advanceTimersByTimeAsync(ANALYSIS_POLL_MS * 3)
    expect(fetch).toHaveBeenCalledTimes(3)
  })

  it('reads an analysis that is already done once, without a refresh', async () => {
    const { fetch, onDone, poller } = setup([payload('done')])
    poller.hold('s1')
    await vi.advanceTimersByTimeAsync(ANALYSIS_POLL_MS * 2)
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(onDone).not.toHaveBeenCalled()
  })

  it('shares one poll between two views', async () => {
    const { fetch, poller } = setup([payload('done')])
    const releaseA = poller.hold('s1')
    const releaseB = poller.hold('s1')
    await vi.advanceTimersByTimeAsync(0)
    expect(fetch).toHaveBeenCalledTimes(1)
    releaseA()
    releaseB()
  })

  it('keeps polling after a transient error, and stops on a 4xx', async () => {
    const { fetch, poller } = setup([httpError(502), httpError(403)])
    poller.hold('s1')
    await vi.advanceTimersByTimeAsync(0)
    expect(poller.snapshot('s1').error).toBe('HTTP 502')
    await vi.advanceTimersByTimeAsync(ANALYSIS_POLL_MS)
    expect(fetch).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(ANALYSIS_POLL_MS * 3)
    expect(fetch).toHaveBeenCalledTimes(2)
  })

  it('stops when the last view lets go', async () => {
    const { fetch, poller } = setup([payload('running'), payload('running')])
    const release = poller.hold('s1')
    await vi.advanceTimersByTimeAsync(0)
    release()
    await vi.advanceTimersByTimeAsync(ANALYSIS_POLL_MS * 3)
    expect(fetch).toHaveBeenCalledTimes(1)
  })

  it('retries a failed job and polls the new one', async () => {
    const retry = vi.fn().mockResolvedValue(payload('pending'))
    const { fetch, poller } = setup([payload('failed'), payload('done')], { retry })
    poller.hold('s1')
    await vi.advanceTimersByTimeAsync(0)
    const pending = poller.retry('s1')
    expect(poller.snapshot('s1').retrying).toBe(true)
    await pending
    expect(poller.snapshot('s1')).toMatchObject({ retrying: false, analysis: { status: 'pending' } })
    await vi.advanceTimersByTimeAsync(ANALYSIS_POLL_MS)
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(poller.snapshot('s1').analysis.status).toBe('done')
  })

  it('reports a failed retry and stays put', async () => {
    const onRetryError = vi.fn()
    const retry = vi.fn().mockRejectedValue(httpError(500))
    const { poller } = setup([payload('failed')], { retry, onRetryError })
    poller.hold('s1')
    await vi.advanceTimersByTimeAsync(0)
    await poller.retry('s1')
    expect(onRetryError).toHaveBeenCalled()
    expect(poller.snapshot('s1')).toMatchObject({ retrying: false, analysis: { status: 'failed' } })
  })
})
