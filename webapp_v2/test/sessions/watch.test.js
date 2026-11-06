import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const api = { get: vi.fn(), post: vi.fn() }
vi.mock('@/services/api', () => ({ default: api }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: vi.fn() } }))
vi.mock('@/utils/snackbar', () => ({ showSnackbar: vi.fn() }))
globalThis.window = new EventTarget()
const { useSessionDetailsStore } = await import('@/stores/useSessionDetailsStore')

beforeEach(() => {
  vi.useFakeTimers()
  useSessionDetailsStore.getState().reset()
})
afterEach(() => {
  vi.useRealTimers()
  vi.clearAllMocks()
})

it('a poll that answers after the modal moved on keeps the new session watch', async () => {
  const store = useSessionDetailsStore
  store.setState({ sessionId: 'a', executions: { a: { status: 'running', attempts: 0 } } })
  let answerA
  api.get.mockReturnValueOnce(new Promise((resolve) => (answerA = resolve)))
  const pollA = store.getState().watch('a')
  // The user opens session b, an exec still running: its watch starts.
  store.setState({ sessionId: 'b', executions: { b: { status: 'running', attempts: 0 } } })
  store.getState().scheduleWatch('b')
  answerA({ data: { status: 'open' } })
  await pollA
  api.get.mockResolvedValue({ data: { status: 'open' } })
  await vi.advanceTimersByTimeAsync(5000)
  expect(api.get).toHaveBeenLastCalledWith('/sessions/b', { params: undefined })
})
