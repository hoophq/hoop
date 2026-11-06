import { describe, expect, it, vi } from 'vitest'
import { NAV, QUICK_ACCESS_ITEMS, SUGGESTION_ITEMS } from '@/layout/Sidebar/gatewayNav'
import { AGENT_PATHS, isAgentPath } from '@/modes/agentPaths'

vi.mock('@/modes/agents', () => ({ AGENTS_FLAG: 'experimental.agents' }))

const entries = (items, flagged = false) =>
  items.flatMap((item) => {
    const own = flagged || item.featureFlag === 'experimental.agents'
    return [...(item.path ? [{ path: item.path.split('?')[0], flagged: own }] : []), ...entries(item.children ?? [], own)]
  })

const NAV_ENTRIES = entries([...NAV.flatMap((section) => section.items), ...SUGGESTION_ITEMS, ...QUICK_ACCESS_ITEMS])

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

  // React Router matches routes ignoring case, after decoding %xx, with trailing slashes.
  it.each(['/Agents', '/AGENTS/new', '/%61gents', '/%41gents', '/Features/Event-Routing', '/agents/', '/agents//', '/Onboarding/setup'])(
    'closes the route spelled %s',
    (pathname) => {
      expect(isAgentPath(pathname)).toBe(true)
    },
  )

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
    '/%E0%A4%A',
  ])('keeps %s', (pathname) => {
    expect(isAgentPath(pathname)).toBe(false)
  })

  it('closes every nav and palette entry the flag hides, and only those', () => {
    expect(AGENT_PATHS.length).toBeGreaterThan(0)
    for (const { path, flagged } of NAV_ENTRIES) {
      expect([path, isAgentPath(path)]).toEqual([path, flagged])
    }
  })
})
