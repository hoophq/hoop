import { useLocation, matchPath } from 'react-router-dom'
import Router from './Router'
import ConnectedCommandPalette from '@/features/CommandPalette'

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
