import { beforeEach, describe, expect, it, vi } from 'vitest'

// The network is the seam: the real auth service, api.js and stores run, and
// fetch answers /userinfo as the gateway would for the token it carries.
const storage = new Map()
vi.stubGlobal('localStorage', {
  getItem: (key) => (storage.has(key) ? storage.get(key) : null),
  setItem: (key, value) => storage.set(key, String(value)),
  removeItem: (key) => storage.delete(key),
})
const listeners = {}
const reload = vi.fn()
vi.stubGlobal('window', {
  addEventListener: (type, fn) => {
    listeners[type] = fn
  },
  location: { protocol: 'http:', host: 'gateway.test', href: 'http://gateway.test/sidecars', reload },
})
vi.mock('@/services/analytics', () => ({ identify: () => Promise.resolve() }))

const { useUserStore } = await import('@/stores/useUserStore')

// The account each token belongs to; a token not listed gets a 401.
const owners = { 'token-of-a': 'user-a', 'renewed-token-of-a': 'user-a', 'token-of-b': 'user-b' }
// Per token, a promise the test holds back to answer late.
let held
const fetch = vi.fn(async (url, init) => {
  const token = init.headers.Authorization.replace(/^Bearer /, '')
  await held[token]
  const id = owners[token]
  return {
    ok: !!id,
    status: id ? 200 : 401,
    headers: new Headers(),
    json: async () => ({ id }),
  }
})
vi.stubGlobal('fetch', fetch)

// What another tab does: write localStorage, then this tab gets the event.
const otherTab = (key, value) => {
  if (value === null) storage.delete(key)
  else storage.set(key, value)
  return listeners.storage({ key, newValue: value })
}

beforeEach(() => {
  fetch.mockClear()
  reload.mockClear()
  held = {}
  storage.set('jwt-token', 'token-of-a')
  useUserStore.setState({ user: { id: 'user-a' } })
})

describe('a token another tab stores', () => {
  it('of another account reloads this tab', async () => {
    await otherTab('jwt-token', 'token-of-b')
    expect(fetch.mock.calls[0][0]).toBe('http://gateway.test/api/userinfo')
    expect(reload).toHaveBeenCalledOnce()
  })

  it('of the same account (a renewal) changes nothing', async () => {
    await otherTab('jwt-token', 'renewed-token-of-a')
    expect(fetch).toHaveBeenCalledOnce()
    expect(reload).not.toHaveBeenCalled()
  })

  it('replaced before its check answers does not reload: the newer token decides', async () => {
    // B, then A again, and the check of B answers last.
    let answerB
    held['token-of-b'] = new Promise((resolve) => {
      answerB = resolve
    })
    const checkB = otherTab('jwt-token', 'token-of-b')
    await otherTab('jwt-token', 'token-of-a')
    answerB()
    await checkB
    expect(reload).not.toHaveBeenCalled()
  })

  it('that the gateway refuses does not sign out the token stored after it', async () => {
    let answerBad
    held['bad-token'] = new Promise((resolve) => {
      answerBad = resolve
    })
    const checkBad = otherTab('jwt-token', 'bad-token')
    await otherTab('jwt-token', 'token-of-a')
    answerBad()
    await checkBad
    expect(localStorage.getItem('jwt-token')).toBe('token-of-a')
    expect(window.location.href).toBe('http://gateway.test/sidecars')
    expect(reload).not.toHaveBeenCalled()
  })

  it('is not checked while this tab shows no user', async () => {
    useUserStore.setState({ user: null })
    await otherTab('jwt-token', 'token-of-b')
    expect(fetch).not.toHaveBeenCalled()
  })

  it('is not checked on a logout or another key', async () => {
    await otherTab('jwt-token', null)
    await otherTab('redirect-after-auth', '/sessions')
    expect(fetch).not.toHaveBeenCalled()
  })
})
