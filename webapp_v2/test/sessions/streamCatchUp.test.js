import { expect, it, vi } from 'vitest'

// The hook runs outside React here: an effect runs at once, state is dropped.
vi.mock('react', () => ({
  useState: (value) => [value, () => {}],
  useRef: (value) => ({ current: value }),
  useMemo: (fn) => fn(),
  useEffect: (fn) => {
    fn()
  },
}))
const api = { get: vi.fn(), defaults: { baseURL: '/api' } }
vi.mock('@/services/api', () => ({ default: api, storeRenewedToken: () => {} }))
vi.mock('@/stores/useAuthStore', () => ({
  useAuthStore: { getState: () => ({ getToken: () => 't' }), subscribe: () => {} },
}))
const patchSession = vi.fn()
vi.mock('@/stores/useSessionDetailsStore', () => ({
  useSessionDetailsStore: { getState: () => ({ patchSession }) },
}))
// A reconnect after the session ended: the gateway answers session_end at once.
vi.mock('@/features/SessionDetails/viewers/recording/sessionStream', () => ({
  StreamRefused: class extends Error {},
  readSessionStream: async (_url, { onOpen }) => {
    onOpen()
  },
}))

it('saves the history read on (re)connect when session_end comes at once', async () => {
  const gap = [[5, 'o', 'Z2Fw']]
  api.get.mockImplementation(() => new Promise((resolve) => setTimeout(() => resolve({ data: { event_stream: gap } }), 0)))
  const { useSessionStream } = await import('@/features/SessionDetails/viewers/recording/useSessionStream')
  useSessionStream({ id: 's1', event_stream: [], start_date: '2026-11-04T00:00:00Z' }, { enabled: true, catchUp: true })
  await vi.waitFor(() => expect(patchSession).toHaveBeenCalled())
  expect(patchSession.mock.calls.at(-1)[0]).toMatchObject({ status: 'done', event_stream: gap })
})
