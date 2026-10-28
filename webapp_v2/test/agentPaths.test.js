import { describe, expect, it, vi } from 'vitest'
import { isAgentPath } from '@/modes/agentPaths'

vi.mock('@/modes/agents', () => ({ AGENTS_FLAG: 'experimental.agents' }))

describe('isAgentPath', () => {
  it.each([
    '/',
    '/client',
    '/onboarding',
    '/onboarding/setup',
    '/agents',
    '/agents/new',
    '/features/event-routing',
    '/features/event-routing/new',
    '/features/event-routing/abc/edit',
    '/features/machine-identities',
    '/features/machine-identities/new',
    '/resources',
    '/resources/new',
    '/resources/configure/pg',
    '/runbooks',
    '/features/runbooks/setup',
    '/provisioning',
    '/integrations/aws-connect',
    '/integrations/aws-connect/setup',
  ])('closes %s', (pathname) => {
    expect(isAgentPath(pathname)).toBe(true)
  })

  it.each([
    '/sidecars',
    '/sidecars/new',
    '/approvals',
    '/sessions',
    '/guardrails',
    '/features/data-masking',
    '/features/ai-session-analyzer',
    '/ai-agents-identities',
    '/resource-catalog-x',
    '/agentsx',
    '/settings/experimental',
    '/integrations/slack',
  ])('keeps %s', (pathname) => {
    expect(isAgentPath(pathname)).toBe(false)
  })
})
