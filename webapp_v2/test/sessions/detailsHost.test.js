import { afterEach, beforeEach, expect, test, vi } from 'vitest'

// The host runs outside React here: an effect runs at once.
vi.mock('react', () => ({
  lazy: () => () => null,
  Suspense: () => null,
  useEffect: (fn) => {
    fn()
  },
  useRef: (value) => ({ current: value }),
}))
vi.mock('react-router-dom', () => ({ useNavigate: () => () => {}, useLocation: () => ({ pathname: '/sessions' }) }))
const mocks = vi.hoisted(() => ({ enabled: true, open: vi.fn() }))
vi.mock('@/modes/sessions', () => ({ useSessionsReactEnabled: () => mocks.enabled }))
vi.mock('@/stores/useSessionDetailsStore', () => {
  const state = { opened: false, sessionId: null, open: (...args) => mocks.open(...args), reset: () => {} }
  const useSessionDetailsStore = (select) => select(state)
  useSessionDetailsStore.getState = () => state
  return { useSessionDetailsStore }
})

// The ClojureScript bridge opens its own details when nobody cancels the event.
const emit = (sessionId) =>
  window.dispatchEvent(new CustomEvent('hoop:session-details-open', { detail: { sessionId }, cancelable: true }))

beforeEach(() => {
  vi.resetModules()
  vi.stubGlobal('window', new EventTarget())
  mocks.open.mockReset()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

async function mountHost() {
  const { default: SessionDetailsHost } = await import('@/features/SessionDetails')
  SessionDetailsHost()
}

test('the React details take the event, so the ClojureScript ones stay closed', async () => {
  mocks.enabled = true
  await mountHost()
  expect(emit('s1')).toBe(false)
  expect(mocks.open).toHaveBeenCalledWith('s1', expect.any(Object))
})

test('with the flag off nobody takes the event: the ClojureScript details open', async () => {
  mocks.enabled = false
  await mountHost()
  expect(emit('s1')).toBe(true)
  expect(mocks.open).not.toHaveBeenCalled()
})
