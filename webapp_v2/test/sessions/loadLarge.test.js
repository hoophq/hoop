import { afterEach, expect, it, vi } from 'vitest'

const api = { get: vi.fn(), post: vi.fn() }
vi.mock('@/services/api', () => ({ default: api }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: vi.fn() } }))
vi.mock('@/utils/snackbar', () => ({ showSnackbar: vi.fn() }))
const { useSessionDetailsStore } = await import('@/stores/useSessionDetailsStore')

const BIG = 5 * 1024 * 1024

afterEach(() => {
  useSessionDetailsStore.getState().reset()
  vi.clearAllMocks()
})

async function load(base) {
  api.get.mockImplementation((url) => Promise.resolve({ data: url === `/sessions/${base.id}` ? base : { items: [] } }))
  useSessionDetailsStore.setState({ sessionId: base.id })
  await useSessionDetailsStore.getState().load(base.id)
}

it('reads the masking report of a session with both payloads above 4 MB', async () => {
  await load({ id: 'big-2', verb: 'exec', status: 'done', start_date: '2026-11-04T10:00:00Z', event_size: BIG, script_size: BIG })
  expect(api.get).toHaveBeenCalledWith('/reports/sessions', { params: { id: 'big-2', start_date: '2026-11-04' } })
})

it('does not read an RDP session with a large input twice: an empty expand loads the input', async () => {
  await load({ id: 'rdp-2', verb: 'connect', status: 'done', type: 'custom', connection_subtype: 'rdp', script_size: BIG })
  expect(api.get.mock.calls.filter(([url]) => url === '/sessions/rdp-2')).toHaveLength(1)
})
