import { Toaster } from 'sonner'
import Router from './Router'

function App() {
  return (
    <>
      <Router />
      <Toaster position="top-right" richColors closeButton />
    </>
  )
}

export default App
