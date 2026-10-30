import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

// The axios instance is the seam: the real services run, so the test sees the
// request each re-run sends and the response shape the store reads.
const api = { get: vi.fn(), post: vi.fn() }
vi.mock('@/services/api', () => ({ default: api }))
vi.mock('@/stores/useAuthStore', () => ({ useAuthStore: { subscribe: vi.fn() } }))
vi.mock('@/utils/snackbar', () => ({ showSnackbar: vi.fn() }))

// The store tells the ClojureScript list on window; vitest runs in node.
globalThis.window = new EventTarget()
const { SESSION_CHANGED_EVENT, useSessionDetailsStore } = await import('@/stores/useSessionDetailsStore')

const original = {
  id: 'orig-1',
  verb: 'exec',
  connection: 'pg-prod',
  script: { data: 'SELECT 1' },
  status: 'done',
}

beforeEach(() => {
  vi.useFakeTimers()
  useSessionDetailsStore.getState().reset()
  useSessionDetailsStore.setState({ opened: true, sessionId: original.id, session: original, status: 'ready' })
})

afterEach(() => {
  vi.useRealTimers()
  vi.clearAllMocks()
})

describe('re-run (NB05)', () => {
  it('keeps the original session open and records the new one', async () => {
    api.post.mockResolvedValueOnce({ data: { session_id: 'new-2' } })
    await useSessionDetailsStore.getState().rerun({ jira_fields: { summary: 'x' } })

    expect(api.post).toHaveBeenCalledWith('/sessions', {
      script: 'SELECT 1',
      labels: { 're-run-from': 'orig-1' },
      connection: 'pg-prod',
      jira_fields: { summary: 'x' },
    })
    const state = useSessionDetailsStore.getState()
    expect(state.sessionId).toBe('orig-1')
    expect(state.rerunId).toBe('new-2')
    expect(state.rerunning).toBe(false)
  })

  it('links the session a failed run names', async () => {
    api.post.mockRejectedValueOnce({ response: { status: 400, data: { session_id: 'failed-3', message: 'boom' } } })
    await useSessionDetailsStore.getState().rerun()

    const state = useSessionDetailsStore.getState()
    expect(state.sessionId).toBe('orig-1')
    expect(state.rerunId).toBe('failed-3')
  })

  it('does not mark another session the user opened meanwhile', async () => {
    let answer
    api.post.mockReturnValueOnce(new Promise((resolve) => (answer = resolve)))
    const pending = useSessionDetailsStore.getState().rerun()
    useSessionDetailsStore.setState({ sessionId: 'other-4', session: { ...original, id: 'other-4' } })
    answer({ data: { session_id: 'new-5' } })
    await pending

    expect(useSessionDetailsStore.getState().rerunId).toBe(null)
  })
})

describe('the ClojureScript list behind the modal (R06, L15)', () => {
  it('hears each change the details make', async () => {
    const heard = vi.fn()
    window.addEventListener(SESSION_CHANGED_EVENT, heard)
    api.post.mockResolvedValueOnce({ data: { session_id: 'new-6' } })
    await useSessionDetailsStore.getState().rerun()
    expect(heard).not.toHaveBeenCalled()
    vi.runAllTimers()
    expect(heard).toHaveBeenCalledTimes(1)
    window.removeEventListener(SESSION_CHANGED_EVENT, heard)
  })
})
