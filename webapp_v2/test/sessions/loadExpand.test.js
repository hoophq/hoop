import { afterEach, describe, expect, it, vi } from 'vitest'

// The axios instance is the seam: the store's two reads go through it.
const api = { get: vi.fn(), post: vi.fn() }
vi.mock('@/services/api', () => ({ default: api }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: vi.fn() } }))
vi.mock('@/utils/snackbar', () => ({ showSnackbar: vi.fn() }))

const { useSessionDetailsStore } = await import('@/stores/useSessionDetailsStore')

afterEach(() => {
  useSessionDetailsStore.getState().reset()
  vi.clearAllMocks()
})

// The second read of /sessions/:id, after the base read.
async function secondRead(base) {
  api.get.mockImplementation((url, config) =>
    Promise.resolve({ data: url.startsWith('/sessions/') && !config?.params ? base : {} }),
  )
  useSessionDetailsStore.setState({ sessionId: base.id })
  await useSessionDetailsStore.getState().load(base.id)
  return api.get.mock.calls.find(([url, config]) => url === `/sessions/${base.id}` && config?.params)?.[1].params
}

describe('the details read the stream only when a viewer shows it', () => {
  it('skips the stream of an RDP session, open or finished (P2, N05)', async () => {
    const rdp = { id: 'rdp-1', verb: 'connect', type: 'custom', connection_subtype: 'rdp' }
    expect(await secondRead({ ...rdp, status: 'open' })).toEqual({ expand: 'session_input' })
    expect(await secondRead({ ...rdp, status: 'done' })).toEqual({ expand: 'session_input' })
  })

  it('reads the stream of an open terminal session', async () => {
    const params = await secondRead({ id: 'pty-1', verb: 'connect', status: 'open', type: 'custom', connection_subtype: 'bash' })
    expect(params.expand).toBe('event_stream,session_input')
  })
})
