import { beforeEach, describe, expect, it, vi } from 'vitest'

// The axios instance is the seam: the real auth service asks /userinfo, and
// the test answers as the gateway would for the token the other tab stored.
const api = { get: vi.fn() }
vi.mock('@/services/api', () => ({ default: api }))
vi.mock('@/services/analytics', () => ({ identify: () => Promise.resolve() }))

const listeners = {}
const reload = vi.fn()
vi.stubGlobal('window', {
  addEventListener: (type, fn) => {
    listeners[type] = fn
  },
  location: { reload },
})

const { useUserStore } = await import('@/stores/useUserStore')

// What another tab does to localStorage, as this tab sees it.
const otherTab = (key, newValue) => listeners.storage({ key, newValue })

let owner
api.get.mockImplementation((url) => {
  if (url === '/userinfo') return owner()
  throw new Error(`unexpected GET ${url}`)
})

beforeEach(() => {
  api.get.mockClear()
  reload.mockClear()
  useUserStore.setState({ user: { id: 'user-a' } })
  owner = () => Promise.resolve({ data: { id: 'user-a' } })
})

describe('a token another tab stores', () => {
  it('of another account reloads this tab', async () => {
    owner = () => Promise.resolve({ data: { id: 'user-b' } })
    await otherTab('jwt-token', 'token-of-b')
    expect(reload).toHaveBeenCalledOnce()
  })

  it('of the same account (a renewal) changes nothing', async () => {
    await otherTab('jwt-token', 'renewed-token-of-a')
    expect(api.get).toHaveBeenCalledWith('/userinfo')
    expect(reload).not.toHaveBeenCalled()
  })

  it('that the gateway refuses does not reload: the 401 handler signs out', async () => {
    owner = () => Promise.reject(Object.assign(new Error('unauthorized'), { response: { status: 401 } }))
    await otherTab('jwt-token', 'bad-token')
    expect(reload).not.toHaveBeenCalled()
  })

  it('is not checked while this tab shows no user', async () => {
    useUserStore.setState({ user: null })
    await otherTab('jwt-token', 'token-of-b')
    expect(api.get).not.toHaveBeenCalled()
  })

  it('is not checked on a logout or another key', async () => {
    await otherTab('jwt-token', null)
    await otherTab('redirect-after-auth', '/sessions')
    expect(api.get).not.toHaveBeenCalled()
  })
})
