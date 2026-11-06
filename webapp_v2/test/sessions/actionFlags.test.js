import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const api = { get: vi.fn(), post: vi.fn(), put: vi.fn() }
vi.mock('@/services/api', () => ({ default: api }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: vi.fn() } }))
vi.mock('@/utils/snackbar', () => ({ showSnackbar: vi.fn() }))
globalThis.window = new EventTarget()
const { useSessionDetailsStore: store } = await import('@/stores/useSessionDetailsStore')

beforeEach(() => {
  vi.useFakeTimers()
  store.getState().reset()
  api.get.mockImplementation((url) => Promise.resolve({ data: { id: url.split('/').pop() } }))
})
afterEach(() => {
  vi.useRealTimers()
  vi.clearAllMocks()
})

it('keeps the decision controls busy until the refreshed review arrives', async () => {
  const session = { id: 's1', review: { id: 'r1', status: 'PENDING' } }
  store.setState({ opened: true, sessionId: 's1', session, status: 'ready' })
  api.put.mockResolvedValueOnce({ data: {} })
  expect(await store.getState().decide({ status: 'APPROVED' })).toBe(true)
  // The review still reads PENDING here: a second click would decide again.
  expect(store.getState().deciding).toBe(true)
  await vi.advanceTimersByTimeAsync(800)
  expect(store.getState().deciding).toBe(false)
})

it('a failed decision frees the controls for a retry', async () => {
  store.setState({ opened: true, sessionId: 's1', session: { id: 's1', review: { id: 'r1' } }, status: 'ready' })
  api.put.mockRejectedValueOnce(new Error('boom'))
  expect(await store.getState().decide({ status: 'APPROVED' })).toBe(false)
  expect(store.getState().deciding).toBe(false)
})

it('a kill that ends on another session leaves this one busy', async () => {
  store.setState({ opened: true, sessionId: 'a', session: { id: 'a' }, status: 'ready' })
  let answerA
  api.post.mockReturnValueOnce(new Promise((resolve) => (answerA = resolve)))
  const killA = store.getState().kill()
  // open('b') resets the flags; b's own kill is still pending.
  store.setState({ sessionId: 'b', session: { id: 'b' }, killing: false })
  api.post.mockReturnValueOnce(new Promise(() => {}))
  store.getState().kill()
  answerA({ data: {} })
  await killA
  expect(store.getState().killing).toBe(true)
})
