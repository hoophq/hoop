import { Toaster } from 'sonner'
import { useEffect } from 'react'
import Router from './Router'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { useClipboardGuard } from '@/hooks/useClipboardGuard'

function App() {
  // Org control `disable_clipboard_copy_cut`. Installed here and nowhere else:
  // App is the only component mounted on every route (Layout misses
  // /onboarding/*, PageLayout misses the CLJS catch-all).
  useClipboardGuard()

  useEffect(() => {
    useConnectionsMetadataStore.getState().load()
  }, [])

  return (
    <>
      <Router />
      <Toaster position="top-right" />
    </>
  )
}

export default App
