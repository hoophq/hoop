import { describe, expect, it, vi } from 'vitest'
import { demoConnection, onboardingPath } from '@/pages/Onboarding/onboarding'
import { POST_SETUP_PATH } from '@/modes'

vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: {} }))

describe('onboardingPath', () => {
  it.each([
    [{ isAdmin: false, hasConnections: false, protectionProfile: null }, '/client'],
    [{ isAdmin: true, hasConnections: true, protectionProfile: null }, '/client'],
    [{ isAdmin: true, hasConnections: false, protectionProfile: null }, '/onboarding/protection-rules'],
    [{ isAdmin: true, hasConnections: false, protectionProfile: undefined }, '/onboarding/protection-rules'],
    [{ isAdmin: true, hasConnections: false, protectionProfile: 'standard' }, '/onboarding/setup'],
  ])('%o goes to %s', (input, path) => {
    expect(onboardingPath(input)).toBe(path)
  })

  it('starts a new local org at the entry check, not past the protection rules', () => {
    expect(POST_SETUP_PATH).toBe('/onboarding')
  })
})

// connection-postgres-demo in webapp/src/webapp/connections/constants.cljs,
// as :connections->quickstart-create-postgres-demo posts it.
const CLJS_DEMO = {
  name: 'postgres-demo',
  type: 'database',
  subtype: 'postgres',
  access_mode_runbooks: 'enabled',
  access_mode_exec: 'enabled',
  access_mode_connect: 'enabled',
  access_schema: 'enabled',
  agent_id: 'agent-1',
  reviewers: [],
  redact_enabled: true,
  redact_types: [],
  secret: {
    'envvar:DB': 'ZGVsbHN0b3Jl',
    'envvar:HOST': 'ZGVtby1wZy1kYi5jaDcwN3JuYWl6amcudXMtZWFzdC0xLnJkcy5hbWF6b25hd3MuY29t',
    'envvar:INSECURE': 'ZmFsc2U=',
    'envvar:PASS': 'ZG9sbGFyLW1hbmdlci1jYXJvdXNlLUhFQVJURUQ=',
    'envvar:PORT': 'NTQzMg==',
    'envvar:USER': 'ZGVtb3JlYWRvbmx5',
  },
  command: [],
}

describe('demoConnection', () => {
  it('posts the CLJS demo payload', () => {
    expect(demoConnection({ agentId: 'agent-1', profileAttribute: null })).toEqual(CLJS_DEMO)
  })

  it('carries the active protection profile attribute', () => {
    expect(demoConnection({ agentId: 'agent-1', profileAttribute: 'hoop-standard' })).toEqual({
      ...CLJS_DEMO,
      attributes: ['hoop-standard'],
    })
  })
})
