import { afterEach, beforeEach, expect, it, vi } from 'vitest'

const api = { get: vi.fn(), post: vi.fn() }
vi.mock('@/services/api', () => ({ default: api }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: vi.fn() } }))
const snackbar = vi.hoisted(() => vi.fn())
vi.mock('@/utils/snackbar', () => ({ showSnackbar: snackbar }))
globalThis.window = new EventTarget()
const { useSessionDetailsStore } = await import('@/stores/useSessionDetailsStore')

// load() read this session's input above 4 MB without expanding it: the
// second read answered script { data: '' } (gateway parser.go toOpenApiSession).
const large = { id: 'big-1', verb: 'exec', connection: 'pg-prod', script: { data: '' }, status: 'done' }

beforeEach(() => {
  vi.useFakeTimers()
  useSessionDetailsStore.getState().reset()
  useSessionDetailsStore.setState({ opened: true, sessionId: large.id, session: large, status: 'ready', largeInput: true })
})
afterEach(() => {
  vi.useRealTimers()
  vi.clearAllMocks()
})

it('re-runs a large-input session with its input, read for the re-run', async () => {
  api.get.mockResolvedValueOnce({ data: { script: { data: 'SELECT big' } } })
  api.post.mockResolvedValueOnce({ data: { session_id: 'new-1' } })
  await useSessionDetailsStore.getState().rerun()
  expect(api.get).toHaveBeenCalledWith('/sessions/big-1', { params: { expand: 'session_input' } })
  expect(api.post).toHaveBeenCalledWith('/sessions', expect.objectContaining({ script: 'SELECT big' }))
})

it('posts nothing when the input cannot be read', async () => {
  api.get.mockResolvedValueOnce({ data: { script: { data: '' } } })
  await useSessionDetailsStore.getState().rerun()
  expect(api.post).not.toHaveBeenCalled()
  expect(snackbar).toHaveBeenCalledWith(expect.objectContaining({ level: 'error' }))
})
