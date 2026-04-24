import { useLocation, matchPath } from 'react-router-dom'
import Router from './Router'
import ConnectedCommandPalette from '@/features/CommandPalette'

// Routes where React owns the UI. On every other route the ClojureScript app
// renders its own command palette — the Mantine Spotlight would otherwise
// appear unstyled because the CLJS Tailwind/Radix stylesheet is unlayered
// and beats Mantine's @layer mantine rules.
const REACT_ROUTE_PATTERNS = ['/agents', '/agents/new']

function App() {
  const { pathname } = useLocation()
  const isReactRoute = REACT_ROUTE_PATTERNS.some(p => matchPath(p, pathname))

  return (
    <>
      <Router />
      {isReactRoute && <ConnectedCommandPalette />}
    </>
  )
}

export default App
