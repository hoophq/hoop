import { lazy, Suspense, useEffect, useRef } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useSessionsReactEnabled } from '@/modes/sessions'
import { useSessionDetailsStore } from '@/stores/useSessionDetailsStore'

// The viewers (ag-grid, asciinema, the RDP decoder) load with the modal only.
const SessionDetailsModal = lazy(() => import('./SessionDetailsModal'))

// ClojureScript pages (Web Terminal, Runbooks, the sessions list, the workflow
// cards) open the session details with this event on `window`.
// Detail: { sessionId, verb?, closeTo? }. `closeTo` comes from the
// /sessions/:id page: Close goes there, and a re-run follows in the URL.
export const SESSION_DETAILS_OPEN_EVENT = 'hoop:session-details-open'

const sessionPath = (id) => `/sessions/${encodeURIComponent(id)}`

// The one session-details modal of the app, mounted in the gateway layout so it
// opens over React and ClojureScript routes alike.
export default function SessionDetailsHost() {
  const enabled = useSessionsReactEnabled()
  const opened = useSessionDetailsStore((s) => s.opened)
  const navigate = useNavigate()
  const { pathname } = useLocation()
  // The page path the details were opened from, when they belong to it.
  const pagePath = useRef(null)

  useEffect(() => {
    if (!enabled) return undefined
    const onOpen = (event) => {
      const { sessionId, verb, closeTo } = event.detail || {}
      if (!sessionId) return
      const state = useSessionDetailsStore.getState()
      // The /sessions/:id page asks again on every render.
      if (state.opened && state.sessionId === sessionId) return
      pagePath.current = closeTo ? sessionPath(sessionId) : null
      state.open(sessionId, {
        prefill: verb ? { verb } : null,
        onClose: closeTo ? () => navigate(closeTo) : null,
        onNavigate: closeTo ? (id) => navigate(sessionPath(id)) : null,
      })
    }
    window.addEventListener(SESSION_DETAILS_OPEN_EVENT, onOpen)
    return () => window.removeEventListener(SESSION_DETAILS_OPEN_EVENT, onOpen)
  }, [enabled, navigate])

  // Leaving the page by another way (Back, a sidebar link) closes the details
  // without the Close navigation. A move to another /sessions/:id is a re-run.
  useEffect(() => {
    const from = pagePath.current
    if (!from || pathname === from) return
    if (/^\/sessions\/(?!filtered$)[^/]+$/.test(pathname)) return
    pagePath.current = null
    const state = useSessionDetailsStore.getState()
    if (state.opened) state.reset()
  }, [pathname])

  if (!enabled || !opened) return null
  return (
    <Suspense fallback={null}>
      <SessionDetailsModal />
    </Suspense>
  )
}
