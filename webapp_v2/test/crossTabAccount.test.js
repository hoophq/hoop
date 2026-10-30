import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// The network is the seam: the real auth service, api.js and stores run, and
// fetch answers /userinfo as the gateway would for the token it carries.
const storage = new Map()
const setItem = vi.fn((key, value) => storage.set(key, String(value)))
vi.stubGlobal('localStorage', {
  getItem: (key) => (storage.has(key) ? storage.get(key) : null),
  setItem,
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
const { useAuthStore } = await import('@/stores/useAuthStore')
const { watchAccountSwitch } = await import('@/features/AccountSwitch')
watchAccountSwitch()

// A JWT names its account in `sub`; `n` tells two tokens of one account apart.
const base64url = (json) => btoa(JSON.stringify(json)).replace(/=+$/, '').replace(/\+/g, '-').replace(/\//g, '_')
const jwt = (sub, n = 1) => `h.${base64url({ sub, n })}.s`

// Opaque tokens: the account each one belongs to; one not listed gets a 401.
const owners = { 'opaque-a': 'user-a', 'opaque-a-2': 'user-a', 'opaque-b': 'user-b' }
// Per token, a promise the test holds back to answer late, the renewal the
// gateway sends in X-New-Access-Token, and how many requests fail first.
let held
let renewals
let networkFailures
const fetch = vi.fn(async (url, init) => {
  const token = init.headers.Authorization.replace(/^Bearer /, '')
  await held[token]
  if (networkFailures[token] > 0) {
    networkFailures[token]--
    throw new TypeError('Failed to fetch')
  }
  const id = owners[token]
  return {
    ok: !!id,
    status: id ? 200 : 401,
    headers: new Headers(renewals[token] ? { 'X-New-Access-Token': renewals[token] } : {}),
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

// This tab signed in with `token`, as user-a.
function signedIn(token) {
  storage.set('jwt-token', token)
  useAuthStore.setState({ token, isAuthenticated: true })
  useUserStore.setState({ user: { id: 'user-a' } })
}

beforeEach(() => {
  fetch.mockClear()
  reload.mockClear()
  held = {}
  renewals = {}
  networkFailures = {}
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('a JWT another tab stores', () => {
  beforeEach(() => signedIn(jwt('sub-a')))

  it('of another account reloads this tab, with no request', async () => {
    await otherTab('jwt-token', jwt('sub-b'))
    expect(reload).toHaveBeenCalledOnce()
    expect(fetch).not.toHaveBeenCalled()
  })

  it('of the same account (a renewal) changes nothing, with no request', async () => {
    await otherTab('jwt-token', jwt('sub-a', 2))
    expect(reload).not.toHaveBeenCalled()
    expect(fetch).not.toHaveBeenCalled()
  })

  it('replaced before its event arrives does not reload: the newer token decides', async () => {
    storage.set('jwt-token', jwt('sub-a', 3))
    await listeners.storage({ key: 'jwt-token', newValue: jwt('sub-b') })
    expect(reload).not.toHaveBeenCalled()
  })
})

describe('an opaque token another tab stores', () => {
  beforeEach(() => signedIn('opaque-a'))

  it('of another account reloads this tab', async () => {
    await otherTab('jwt-token', 'opaque-b')
    expect(fetch.mock.calls[0][0]).toBe('http://gateway.test/api/userinfo')
    expect(reload).toHaveBeenCalledOnce()
  })

  it('of the same account (a renewal) changes nothing', async () => {
    await otherTab('jwt-token', 'opaque-a-2')
    expect(fetch).toHaveBeenCalledOnce()
    expect(reload).not.toHaveBeenCalled()
  })

  it('is checked without writing a token, even when the answer renews it', async () => {
    // A write here fires the same check in the other tab: two tabs would loop.
    renewals['opaque-a-2'] = 'opaque-a-3'
    renewals['opaque-b'] = 'opaque-b-2'
    setItem.mockClear()
    await otherTab('jwt-token', 'opaque-a-2')
    await otherTab('jwt-token', 'opaque-b')
    expect(setItem).not.toHaveBeenCalled()
    expect(reload).toHaveBeenCalledOnce()
  })

  it('replaced before its check answers does not reload: the newer token decides', async () => {
    let answerB
    held['opaque-b'] = new Promise((resolve) => {
      answerB = resolve
    })
    const checkB = otherTab('jwt-token', 'opaque-b')
    await otherTab('jwt-token', 'opaque-a-2')
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
    await otherTab('jwt-token', 'opaque-a-2')
    answerBad()
    await checkBad
    expect(localStorage.getItem('jwt-token')).toBe('opaque-a-2')
    expect(window.location.href).toBe('http://gateway.test/sidecars')
    expect(reload).not.toHaveBeenCalled()
  })

  it('that the network fails to check is checked again until the gateway answers', async () => {
    vi.useFakeTimers()
    vi.spyOn(Math, 'random').mockReturnValue(0)
    networkFailures['opaque-b'] = 2
    const check = otherTab('jwt-token', 'opaque-b')
    await vi.advanceTimersByTimeAsync(3000)
    expect(fetch).toHaveBeenCalledTimes(2)
    expect(reload).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(3000)
    await check
    expect(fetch).toHaveBeenCalledTimes(3)
    expect(reload).toHaveBeenCalledOnce()
  })

  it('that the network fails to check is not checked again once replaced', async () => {
    vi.useFakeTimers()
    vi.spyOn(Math, 'random').mockReturnValue(0)
    networkFailures['opaque-b'] = 1
    const check = otherTab('jwt-token', 'opaque-b')
    await vi.advanceTimersByTimeAsync(0)
    await otherTab('jwt-token', 'opaque-a-2')
    await vi.advanceTimersByTimeAsync(3000)
    await check
    expect(fetch.mock.calls.map(([, init]) => init.headers.Authorization)).toEqual([
      'Bearer opaque-b',
      'Bearer opaque-a-2',
    ])
    expect(reload).not.toHaveBeenCalled()
  })

  it('is not checked while this tab shows no user', async () => {
    useUserStore.setState({ user: null })
    await otherTab('jwt-token', 'opaque-b')
    expect(fetch).not.toHaveBeenCalled()
  })

  it('is not checked on a logout or another key', async () => {
    await otherTab('jwt-token', null)
    await otherTab('redirect-after-auth', '/sessions')
    expect(fetch).not.toHaveBeenCalled()
  })
})
