import { Toaster } from 'sonner'
import { useEffect } from 'react'
import Router from './Router'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'

function App() {
  
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
