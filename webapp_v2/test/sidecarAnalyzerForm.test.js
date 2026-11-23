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

const { specToForm, formToSpec, approverConfigured, handoffConfigured, humanLevelsWithoutMode, APPROVER_RULE } =
  await import('@/pages/Features/AiSessionAnalyzer/Create/useSidecarAiAnalyzerEditor')
const {
  ROUTING_AI,
  ROUTING_STATIC,
  emptyRouting,
  readRouting,
  routingToSpec,
  routingModePatch,
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

  it('starts a human hold with no way to approve, and refuses to save it', () => {
    const form = { ...specToForm(null, 'r', []), high: 'require_approval' }
    form.approval.high = { ...form.approval.high, approver: 'human' }
    expect(form.approval.high.routing.mode).toBeNull()
    expect(approverConfigured(form.approval.high)).toBe(false)
    expect(humanLevelsWithoutMode(form)).toEqual(['high'])
    expect(formToSpec(form, 'r').approvers).toBeUndefined()
    form.approval.high.routing.mode = ROUTING_AI
    expect(humanLevelsWithoutMode(form)).toEqual([])
    expect(formToSpec(form, 'r').approvers.high).toEqual({ type: 'human', routing: {} })
  })
})

describe('the candidate groups of an AI-routed level', () => {
  it('every box checked saves no candidates, and none stored checks every box', () => {
    const routing = emptyRouting(ROUTING_AI)
    expect(candidateSelection(routing.candidates, GROUPS)).toEqual(GROUPS)
    routing.candidates = selectCandidates(GROUPS, GROUPS, true)
    expect(routing.candidates).toBeNull()
    expect(routingToSpec(routing)).toEqual({})
    expect(candidateSelection(readRouting({}).candidates, GROUPS)).toEqual(GROUPS)
    expect(candidateSelection(readRouting({ candidates: [] }).candidates, GROUPS)).toEqual(GROUPS)
  })

  it('keeps the explicit list while the org groups have not loaded', () => {
    // Before the load, the options are the level's own stored names: all of
    // them checked is not every group.
    expect(selectCandidates(['engineering'], ['engineering'], false)).toEqual(['engineering'])
    expect(selectCandidates(['engineering'], ['engineering'], true)).toBeNull()
  })

  it('a partial selection is saved and read back as it is', () => {
    const routing = emptyRouting(ROUTING_AI)
    routing.candidates = selectCandidates(['engineering'], GROUPS, true)
    expect(routingToSpec(routing)).toEqual({ candidates: ['engineering'] })
    expect(candidateSelection(readRouting({ candidates: ['engineering'] }).candidates, GROUPS)).toEqual([
      'engineering',
    ])
  })

  it('a cleared selection saves as every group', () => {
    const routing = emptyRouting(ROUTING_AI)
    routing.candidates = selectCandidates([], GROUPS, true)
    expect(routing.candidates).toEqual([])
    expect(routingToSpec(routing)).toEqual({})
  })

  it('a stored human level with groups is "By groups"', () => {
    const spec = { high: 'require_approval', approvers: { high: { type: 'human', groups: ['engineering'] } } }
    const form = specToForm(spec, 'r', [])
    expect(form.approval.high.routing.mode).toBe(ROUTING_STATIC)
    expect(formToSpec(form, 'r').approvers.high).toEqual({ type: 'human', groups: ['engineering'] })
  })

  it('carries the selection over a change of "How approval is done"', () => {
    const ai = { approver: 'human', groups: [], routing: { ...emptyRouting(ROUTING_AI), candidates: ['engineering'] } }
    const toStatic = routingModePatch(ai, ROUTING_STATIC, GROUPS, true)
    expect(toStatic.routing.mode).toBe(ROUTING_STATIC)
    expect(toStatic.groups).toEqual(['engineering'])
    // All candidates seed every group.
    expect(routingModePatch({ ...ai, routing: emptyRouting(ROUTING_AI) }, ROUTING_STATIC, GROUPS, true).groups).toEqual(
      GROUPS,
    )
    // Groups already chosen stay.
    expect(routingModePatch({ ...ai, groups: ['security-analysts'] }, ROUTING_STATIC, GROUPS, true).groups).toBeUndefined()

    const stat = { approver: 'human', groups: ['engineering'], routing: emptyRouting(ROUTING_STATIC) }
    const toAI = routingModePatch(stat, ROUTING_AI, GROUPS, true)
    expect(toAI.routing.mode).toBe(ROUTING_AI)
    expect(toAI.routing.candidates).toEqual(['engineering'])
    expect(toAI.groups).toBeUndefined()
    expect(routingModePatch({ ...stat, groups: GROUPS }, ROUTING_AI, GROUPS, true).routing.candidates).toBeNull()
    // Candidates already chosen stay.
    const kept = { ...stat, routing: { ...emptyRouting(ROUTING_STATIC), candidates: ['security-analysts'] } }
    expect(routingModePatch(kept, ROUTING_AI, GROUPS, true).routing.candidates).toEqual(['security-analysts'])
  })

  it('keeps what an empty selection means over a change of "How approval is done"', () => {
    // By groups with no group means the administrators, and stays so after AI Decides.
    const spec = { high: 'require_approval', approvers: { high: { type: 'human' } } }
    const stored = specToForm(spec, 'r', []).approval.high
    expect(stored.routing.mode).toBe(ROUTING_STATIC)
    const toAI = { ...stored, ...routingModePatch(stored, ROUTING_AI, GROUPS, true) }
    expect(toAI.routing.candidates).toBeNull()
    expect(routingModePatch(toAI, ROUTING_STATIC, GROUPS, true).groups).toBeUndefined()

    // AI Decides with the candidates cleared means every group, and so does By groups.
    const cleared = { approver: 'human', groups: [], groupsChosen: false, routing: emptyRouting(ROUTING_AI) }
    cleared.routing.candidates = selectCandidates([], GROUPS, true)
    const toStatic = routingModePatch(cleared, ROUTING_STATIC, GROUPS, true)
    expect(toStatic.groups).toEqual(GROUPS)
    expect(toStatic.groupsChosen).toBe(true)

    // Before the groups load there is nothing to seed: the editor seeds later.
    const pending = routingModePatch(cleared, ROUTING_STATIC, [], false)
    expect(pending.groups).toEqual([])
    expect(pending.groupsChosen).toBe(false)
  })

  it('turns a stored AI agent into a person with no selection made', () => {
    const spec = { high: 'require_approval', approvers: { high: { type: 'ai_agent' } } }
    const stored = specToForm(spec, 'r', []).approval.high
    expect(stored.groupsChosen).toBe(false)
    const human = { ...stored, approver: 'human' }
    expect(human.routing.mode).toBeNull()
    expect(routingModePatch(human, ROUTING_STATIC, GROUPS, true).groups).toEqual(GROUPS)
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
