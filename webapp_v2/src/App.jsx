import { Toaster } from 'sonner'
import { useEffect } from 'react'
import Router from './Router'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'

// Clears the global header so toasts never cover the Native Connections button
// or the user menu. Falls back to sonner's default 24px gap on routes rendered
// outside the AppShell (auth, onboarding), where the variable does not exist.
const TOAST_OFFSET = { top: 'calc(var(--app-shell-header-offset, 0rem) + 1.5rem)' }

function App() {

  useEffect(() => {
    useConnectionsMetadataStore.getState().load()
  }, [])

  return (
    <>
      <Router />
      <Toaster position="top-right" offset={TOAST_OFFSET} />
    </>
  )
}

export default App
