import { useEffect } from 'react'
import { useParams } from 'react-router-dom'
import { isReactSetup, setupConfigForResource } from '@/pages/Resources/Setup/catalog'
import { useResourceSetupStore } from '@/pages/Resources/Setup/store'

/**
 * Picks the React or the CLJS add-role page for /resources/:resourceName/add-role.
 * Used in Router.jsx only:
 *
 *   <ByAddRoleSetup react={<Page adminOnly><ResourceAddRole /></Page>} cljs={CatchAll} />
 *
 * It reads the resource into `target`. Once read, a subtype outside
 * REACT_SETUP_SUBTYPES renders the CLJS page. Until then, and for an unknown
 * resource or a failed read, the React page renders inside the shell and shows it.
 */
export default function ByAddRoleSetup({ react, cljs }) {
  const { resourceName } = useParams()
  const target = useResourceSetupStore((s) => s.target)
  const loadTarget = useResourceSetupStore((s) => s.loadTarget)
  const clearTarget = useResourceSetupStore((s) => s.clearTarget)

  useEffect(() => {
    loadTarget(resourceName)
    return clearTarget
  }, [resourceName, loadTarget, clearTarget])

  const read = target.name === resourceName && target.status === 'ready'
  if (read && !isReactSetup(setupConfigForResource(target.resource))) return cljs
  return react
}
