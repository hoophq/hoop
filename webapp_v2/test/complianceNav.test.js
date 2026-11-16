import { describe, expect, it, vi } from 'vitest'
import { NAV } from '@/layout/Sidebar/gatewayNav'

vi.mock('@/modes/agents', () => ({ AGENTS_FLAG: 'experimental.agents' }))

const items = (list) => list.flatMap((item) => [item, ...items(item.children ?? [])])
const all = items(NAV.flatMap((section) => section.items))

describe('Compliance Packs menu item', () => {
  it('opens the protection rules page at /compliance-packs, not Rulepacks', () => {
    const item = all.find((i) => i.label === 'Compliance Packs')
    expect(item.path).toBe('/compliance-packs')
    expect(item.featureFlag).toBeUndefined()
  })

  it('is the only menu entry for that page', () => {
    expect(all.filter((i) => i.path === '/settings/protection-rules')).toEqual([])
    expect(all.filter((i) => i.path === '/rulepacks')).toEqual([])
  })
})
