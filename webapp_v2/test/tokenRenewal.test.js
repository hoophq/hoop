import { AxiosError } from 'axios'
import { beforeEach, describe, expect, it, vi } from 'vitest'

// The network is the seam: the real axios instance, store and services run,
// and only the adapter (axios) and fetch (the SSE clients) are fake.
const storage = new Map()
vi.stubGlobal('localStorage', {
  getItem: (key) => (storage.has(key) ? storage.get(key) : null),
  setItem: (key, value) => storage.set(key, String(value)),
  removeItem: (key) => storage.delete(key),
})
vi.stubGlobal('window', { location: { protocol: 'http:', host: 'gateway.test', href: 'http://gateway.test/sessions' } })
vi.mock('@/stores/useUserStore', () => ({ useUserStore: { getState: () => ({ clear: () => {} }) } }))

storage.set('jwt-token', 'token-1')
const { default: api } = await import('@/services/api')
const { useAuthStore } = await import('@/stores/useAuthStore')
const { liveEventsService } = await import('@/services/liveEvents')
const { serverLogsService } = await import('@/services/serverLogs')

// The gateway: answers with `status`, and with `renewed` in X-New-Access-Token.
let reply
let sent
const gateway = (config) => {
  sent.push(config.headers.Authorization)
  const { status, renewed } = reply
  const response = {
    data: {},
    status,
    statusText: '',
    headers: renewed ? { 'x-new-access-token': renewed } : {},
    config,
    request: {},
  }
  if (status < 400) return Promise.resolve(response)
  return Promise.reject(new AxiosError('failed', AxiosError.ERR_BAD_REQUEST, config, {}, response))
}

beforeEach(() => {
  api.defaults.adapter = gateway
  storage.set('jwt-token', 'token-1')
  useAuthStore.setState({ token: 'token-1', isAuthenticated: true })
  reply = { status: 200 }
  sent = []
})

describe('X-New-Access-Token on an API response', () => {
  it('stores the renewed token, and the next request sends it', async () => {
    reply = { status: 200, renewed: 'token-2' }
    await api.get('/userinfo')
    expect(localStorage.getItem('jwt-token')).toBe('token-2')

    reply = { status: 200 }
    await api.get('/userinfo')
    expect(sent).toEqual(['Bearer token-1', 'Bearer token-2'])
  })

  it('stores the renewed token from an error response', async () => {
    // The gateway rotates before the handler runs, so a 404 can carry it.
    reply = { status: 404, renewed: 'token-2' }
    await expect(api.get('/connections/gone')).rejects.toThrow()
    expect(localStorage.getItem('jwt-token')).toBe('token-2')
  })

  it('is not an account change: the stores that reset on one are not told', async () => {
    const listener = vi.fn()
    const unsubscribe = useAuthStore.subscribe(listener)
    reply = { status: 200, renewed: 'token-2' }
    await api.get('/userinfo')
    unsubscribe()
    expect(listener).not.toHaveBeenCalled()
  })

  it('does not bring the token back after a logout', async () => {
    let answer
    api.defaults.adapter = (config) =>
      new Promise((resolve) => {
        answer = () => resolve({ data: {}, status: 200, headers: { 'x-new-access-token': 'token-2' }, config })
      })
    const pending = api.get('/userinfo')
    await vi.waitFor(() => expect(answer).toBeDefined())
    useAuthStore.getState().logout()
    answer()
    await pending
    expect(localStorage.getItem('jwt-token')).toBeNull()
  })

  it('does not replace the token of another sign-in', () => {
    useAuthStore.getState().setToken('other-account')
    useAuthStore.getState().renewToken('token-1', 'token-2')
    expect(localStorage.getItem('jwt-token')).toBe('other-account')
  })
})

describe('a token the CLJS app stored', () => {
  it('is the one the next React request sends', async () => {
    // webapp/src/webapp/http/request.cljs writes localStorage and nothing else.
    localStorage.setItem('jwt-token', 'renewed-by-cljs')
    await api.get('/userinfo')
    expect(sent).toEqual(['Bearer renewed-by-cljs'])
  })
})

describe('the SSE clients', () => {
  const streams = [
    ['liveEventsService', (signal) => liveEventsService.stream({ signal, onEvent: () => {} })],
    ['serverLogsService', (signal) => serverLogsService.stream({ signal, onEntry: () => {} })],
  ]

  it.each(streams)('%s sends the current token on each connect and stores a renewal', async (_, connect) => {
    const fetch = vi.fn(async () => ({
      ok: true,
      status: 200,
      headers: new Headers(fetch.mock.calls.length === 1 ? { 'X-New-Access-Token': 'token-2' } : {}),
      body: { getReader: () => ({ read: async () => ({ done: true }) }) },
    }))
    vi.stubGlobal('fetch', fetch)
    const signal = new AbortController().signal

    await connect(signal)
    expect(localStorage.getItem('jwt-token')).toBe('token-2')
    localStorage.setItem('jwt-token', 'renewed-by-cljs')
    await connect(signal)

    const tokens = fetch.mock.calls.map(([, init]) => init.headers.Authorization)
    expect(tokens).toEqual(['Bearer token-1', 'Bearer renewed-by-cljs'])
  })
})
