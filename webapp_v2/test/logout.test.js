import { beforeEach, describe, expect, it, vi } from 'vitest'

const storage = new Map()
vi.stubGlobal('localStorage', {
  getItem: (key) => (storage.has(key) ? storage.get(key) : null),
  setItem: (key, value) => storage.set(key, String(value)),
  removeItem: (key) => storage.delete(key),
})
const assign = vi.fn()
vi.stubGlobal('window', { location: { origin: 'https://app.example.test', assign } })

let userState
const clear = vi.fn()
vi.mock('@/stores/useUserStore', () => ({ useUserStore: { getState: () => ({ ...userState, clear }) } }))

const { federatedLogoutUrl, postLogoutPath, useAuthStore } = await import('@/stores/useAuthStore')

const AUTH0 =
  'https://hoophq.us.auth0.com/v2/logout?client_id=DatIOCxntNv8AZrQLVnLb3tr1Y3oVwGW' +
  '&returnTo=https%3A%2F%2Fapp.example.test%2Flogin&federated'

describe('federatedLogoutUrl', () => {
  const origin = 'https://app.example.test'

  it('signs a multi-tenant org out of Auth0, as CLJS :auth->logout', () => {
    expect(federatedLogoutUrl({ tenancyType: 'multitenant', authMethod: 'oidc', origin })).toBe(AUTH0)
  })

  it.each([
    ['multitenant', 'saml'],
    ['selfhosted', 'oidc'],
    ['selfhosted', 'local'],
    [undefined, null],
  ])('leaves %s/%s to /logout', (tenancyType, authMethod) => {
    expect(federatedLogoutUrl({ tenancyType, authMethod, origin })).toBeNull()
  })
})

describe('postLogoutPath', () => {
  it.each([
    ['local', '/login'],
    [null, '/login'],
    [undefined, '/login'],
    ['oidc', '/idplogin'],
    ['saml', '/idplogin'],
  ])('%s goes to %s', (method, path) => {
    expect(postLogoutPath(method)).toBe(path)
  })
})

describe('logout and signOut', () => {
  beforeEach(() => {
    storage.clear()
    storage.set('jwt-token', 'token-1')
    assign.mockClear()
    clear.mockClear()
    userState = { user: { tenancy_type: 'selfhosted' }, authMethod: 'oidc', idpProviderName: 'microsoft-entra-id' }
  })

  it('clears the session and keeps the IdP name for the next sign-in', () => {
    useAuthStore.getState().logout()
    expect(storage.get('jwt-token')).toBeUndefined()
    expect(storage.get('idp-provider-name')).toBe('microsoft-entra-id')
    expect(clear).toHaveBeenCalled()
    expect(useAuthStore.getState().isAuthenticated).toBe(false)
  })

  it('keeps the saved IdP name when /serverinfo has not loaded', () => {
    storage.set('idp-provider-name', 'okta')
    userState = { user: null, authMethod: null, idpProviderName: null }
    useAuthStore.getState().logout()
    expect(storage.get('idp-provider-name')).toBe('okta')
  })

  it('sends a self-hosted org to /logout', () => {
    useAuthStore.getState().signOut()
    expect(storage.get('jwt-token')).toBeUndefined()
    expect(assign).toHaveBeenCalledWith('/logout')
  })

  it('sends a multi-tenant OIDC org to the Auth0 logout, with the session cleared', () => {
    userState = { ...userState, user: { tenancy_type: 'multitenant' } }
    useAuthStore.getState().signOut()
    expect(storage.get('jwt-token')).toBeUndefined()
    expect(assign).toHaveBeenCalledWith(AUTH0)
  })
})
