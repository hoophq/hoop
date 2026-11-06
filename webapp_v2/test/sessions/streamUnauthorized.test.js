import { expect, it, vi } from 'vitest'

vi.mock('react', () => ({
  useState: (value) => [value, () => {}],
  useRef: (value) => ({ current: value }),
  useMemo: (fn) => fn(),
  useEffect: (fn) => {
    fn()
  },
}))
const redirectToLogin = vi.hoisted(() => vi.fn())
vi.mock('@/services/api', () => ({
  default: { get: vi.fn(), defaults: { baseURL: '/api' } },
  storeRenewedToken: () => {},
  redirectToLogin,
}))
vi.mock('@/stores/useAuthStore', () => ({
  useAuthStore: { getState: () => ({ getToken: () => 'expired' }), subscribe: () => {} },
}))
vi.mock('@/stores/useSessionDetailsStore', () => ({
  useSessionDetailsStore: { getState: () => ({ patchSession: () => {} }) },
}))
// The gateway answers 401 with an expired token (a reconnect after a drop).
const answer = vi.hoisted(() => ({ status: 401 }))
globalThis.fetch = vi.fn(async () => ({ ok: false, status: answer.status, body: null, headers: new Headers() }))

it('sends the user to the login page when the stream answers 401', async () => {
  const { useSessionStream } = await import('@/features/SessionDetails/viewers/recording/useSessionStream')
  useSessionStream({ id: 's1', event_stream: [] }, { enabled: true, catchUp: true })
  await vi.waitFor(() => expect(redirectToLogin).toHaveBeenCalledOnce())
})
