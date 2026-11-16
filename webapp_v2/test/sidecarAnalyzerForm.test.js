import { describe, expect, it, vi } from 'vitest'

// The editor module reaches the stores and services on import; the form
// helpers under test are pure.
vi.mock('@/services/users', () => ({ usersService: { listGroups: vi.fn() } }))
vi.mock('@/services/accessRequests', () => ({ accessRequestsService: {} }))
vi.mock('@/services/aiSessionAnalyzer', () => ({ aiSessionAnalyzerService: {} }))
vi.mock('@/services/dataMasking', () => ({ dataMaskingService: {} }))
vi.mock('@/services/guardrails', () => ({ guardrailsService: {} }))
vi.mock('@/services/reviews', () => ({ reviewsService: {} }))
vi.mock('@/services/sidecars', () => ({ sidecarsService: {} }))
vi.mock('@/utils/snackbar', () => ({ showSnackbar: vi.fn() }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: () => () => {} } }))
vi.mock('@/stores/useSidecarStore', () => ({ useSidecarStore: () => [] }))

const { specToForm, formToSpec, approverConfigured, handoffConfigured, APPROVER_RULE } = await import(
  '@/pages/Features/AiSessionAnalyzer/Create/useSidecarAiAnalyzerEditor'
)
const {
  ROUTING_AI,
  ROUTING_STATIC,
  emptyRouting,
  readRouting,
  routingToSpec,
  candidateSelection,
  selectCandidates,
} = await import('@/pages/Features/AiSessionAnalyzer/Create/approvalRouting')
const { firstOpenCard } = await import('@/pages/Features/AiSessionAnalyzer/components/firstOpenCard')

const GROUPS = ['engineering', 'payments-manager', 'security-analysts']

describe('a rule with no trigger field (ENG-1157)', () => {
  it('keeps a stored trigger and max_calls through a save', () => {
    const spec = {
      high: 'block',
      trigger: {
        operations: ['delete'],
        tables: ['customers'],
        any: [{ operations: ['update'], tables: ['orders'] }],
        exclude: [{ tables: ['audit'] }],
      },
      max_calls: 20,
    }
    expect(formToSpec(specToForm(spec, 'r', []), 'r')).toEqual(spec)
  })

  it('writes neither for a new rule', () => {
    const spec = formToSpec({ ...specToForm(null, 'r', []), high: 'block' }, 'r')
    expect(spec).toEqual({ high: 'block' })
  })
})

describe('a new rule', () => {
  it('sets no level, so a save with none set is refused by the form', () => {
    const form = specToForm(null, 'r', [])
    expect([form.high, form.medium, form.low]).toEqual(['', '', ''])
    const spec = formToSpec(form, 'r')
    expect(spec.high ?? spec.medium ?? spec.low).toBeUndefined()
  })

  it('lets the AI decide a new human hold', () => {
    const form = { ...specToForm(null, 'r', []), high: 'require_approval' }
    form.approval.high = { ...form.approval.high, approver: 'human' }
    expect(form.approval.high.routing.mode).toBe(ROUTING_AI)
    expect(formToSpec(form, 'r').approvers.high).toEqual({ type: 'human', routing: {} })
  })
})

describe('the candidate groups of an AI-routed level', () => {
  it('every box checked saves no candidates, and none stored checks every box', () => {
    const routing = emptyRouting()
    expect(candidateSelection(routing.candidates, GROUPS)).toEqual(GROUPS)
    routing.candidates = selectCandidates(GROUPS, GROUPS)
    expect(routing.candidates).toBeNull()
    expect(routingToSpec(routing)).toEqual({})
    expect(candidateSelection(readRouting({}).candidates, GROUPS)).toEqual(GROUPS)
    expect(candidateSelection(readRouting({ candidates: [] }).candidates, GROUPS)).toEqual(GROUPS)
  })

  it('a partial selection is saved and read back as it is', () => {
    const routing = emptyRouting()
    routing.candidates = selectCandidates(['engineering'], GROUPS)
    expect(routingToSpec(routing)).toEqual({ candidates: ['engineering'] })
    expect(candidateSelection(readRouting({ candidates: ['engineering'] }).candidates, GROUPS)).toEqual([
      'engineering',
    ])
  })

  it('a cleared selection saves as every group', () => {
    const routing = emptyRouting()
    routing.candidates = selectCandidates([], GROUPS)
    expect(routing.candidates).toEqual([])
    expect(routingToSpec(routing)).toEqual({})
  })

  it('a stored human level with groups is "By groups"', () => {
    const spec = { high: 'require_approval', approvers: { high: { type: 'human', groups: ['engineering'] } } }
    const form = specToForm(spec, 'r', [])
    expect(form.approval.high.routing.mode).toBe(ROUTING_STATIC)
    expect(formToSpec(form, 'r').approvers.high).toEqual({ type: 'human', groups: ['engineering'] })
  })
})

describe('a level card', () => {
  it('is configured once its approver is chosen, and a person has a way to approve', () => {
    expect(approverConfigured({ approver: 'ai_agent', routing: emptyRouting() })).toBe(true)
    expect(approverConfigured({ approver: APPROVER_RULE, routing: emptyRouting() })).toBe(true)
    expect(approverConfigured({ approver: 'human', routing: emptyRouting(ROUTING_AI) })).toBe(true)
    expect(approverConfigured({ approver: 'human', routing: emptyRouting(null) })).toBe(false)
    expect(approverConfigured({ approver: '', routing: emptyRouting() })).toBe(false)
  })

  it('is configured as a hand-off once an action is allowed', () => {
    expect(handoffConfigured({ allowed: ['warn'], prompt: '' })).toBe(true)
    expect(handoffConfigured({ allowed: [], prompt: '' })).toBe(false)
  })

  it('opens the first card still to configure, else the first', () => {
    expect(firstOpenCard([{ key: 'policy', configured: false }, { key: 'approver', configured: true }])).toBe('policy')
    expect(firstOpenCard([{ key: 'policy', configured: true }, { key: 'approver', configured: false }])).toBe(
      'approver',
    )
    expect(firstOpenCard([{ key: 'policy', configured: true }, { key: 'approver', configured: true }])).toBe('policy')
    expect(firstOpenCard([])).toBeNull()
  })
})
