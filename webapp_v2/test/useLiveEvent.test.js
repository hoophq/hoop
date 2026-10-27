import { afterEach, beforeEach, expect, test, vi } from 'vitest'

// The hook runs outside React here: an effect runs at once.
vi.mock('react', () => ({ useRef: (value) => ({ current: value }), useEffect: (fn) => { fn() } }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: () => {} } }))
vi.mock('@/stores/useUserStore', () => ({
  useUserStore: { subscribe: () => {}, getState: () => ({ user: { id: 'u1' } }) },
}))
const mocks = vi.hoisted(() => ({ stream: () => new Promise(() => {}) }))
vi.mock('@/services/liveEvents', () => ({ liveEventsService: { stream: (opts) => mocks.stream(opts) } }))

class FakeChannel {
  postMessage() {}
  close() {}
}

const flush = async () => {
  for (let i = 0; i < 50; i++) await Promise.resolve()
}

// Loads the hook with navigator.locks.request = request, and subscribes once.
async function subscribe(request) {
  vi.stubGlobal('navigator', { locks: { request } })
  vi.stubGlobal('BroadcastChannel', FakeChannel)
  const { useLiveEvent } = await import('@/hooks/useLiveEvent')
  // react is mocked above, so the hook runs outside a component.
  // eslint-disable-next-line react-hooks/rules-of-hooks
  useLiveEvent('test', () => {})
  await flush()
}

// retryDelay() is 3 s with Math.random() at 0.
const DELAY = 3000

beforeEach(() => {
  vi.resetModules()
  vi.useFakeTimers()
  vi.spyOn(Math, 'random').mockReturnValue(0)
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

test('a lock request that rejects waits the retry delay before the next one', async () => {
  // A document that is not fully active rejects every lock request at once.
  // Past 50 calls the mock stops answering, so a loop fails the test instead of hanging it.
  const request = vi.fn(() => request.mock.calls.length > 50
    ? new Promise(() => {})
    : Promise.reject(new DOMException('The document is not fully active.', 'InvalidStateError')))
  await subscribe(request)
  expect(request).toHaveBeenCalledTimes(1)

  await vi.advanceTimersByTimeAsync(DELAY)
  await flush()
  expect(request).toHaveBeenCalledTimes(2)
})

test('the lock holder reconnects once its own delay ends, not after a second one', async () => {
  // The server ends each stream at once; the holder waits inside the lock.
  mocks.stream = () => Promise.resolve()
  const request = vi.fn((name, options, callback) => callback())
  await subscribe(request)
  expect(request).toHaveBeenCalledTimes(1)

  await vi.advanceTimersByTimeAsync(DELAY)
  await flush()
  expect(request).toHaveBeenCalledTimes(2)
})
