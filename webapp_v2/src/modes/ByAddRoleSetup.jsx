import { useEffect } from 'react'
import { useParams } from 'react-router-dom'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { isReactSetup, setupConfigForResource } from '@/pages/Resources/Setup/catalog'
import { useResourceSetupStore } from '@/pages/Resources/Setup/store'

/**
 * Picks the React or the CLJS add-role page for /resources/:resourceName/add-role.
 * Used in Router.jsx only:
 *
 *   <ByAddRoleSetup react={<Page adminOnly><ResourceAddRole /></Page>} cljs={CatchAll} />
 *
 * A resource whose subtype is in REACT_SETUP_SUBTYPES renders React; any other
 * renders the CLJS page. An unknown resource, or a failed read, renders React,
 * which shows it.
 */
export default function ByAddRoleSetup({ react, cljs }) {
  const { resourceName } = useParams()
  const target = useResourceSetupStore((s) => s.target)
  const loadTarget = useResourceSetupStore((s) => s.loadTarget)
  const clearTarget = useResourceSetupStore((s) => s.clearTarget)
  const metadata = useConnectionsMetadataStore((s) => s.metadata)
  const error = useConnectionsMetadataStore((s) => s.error)
  const load = useConnectionsMetadataStore((s) => s.load)

  useEffect(() => {
    loadTarget(resourceName)
    return clearTarget
  }, [resourceName, loadTarget, clearTarget])

  useEffect(() => {
    load()
  }, [load])

  if (target.name !== resourceName || target.status === 'idle' || target.status === 'loading') {
    return <PageLoader h={400} />
  }
  if (target.status !== 'ready') return react
  if (!isReactSetup(setupConfigForResource(target.resource))) return cljs
  if (!metadata) {
    if (!error) return <PageLoader h={400} />
    return (
      <EmptyState
        title="Could not load the resource catalog"
        description={error}
        action={{ label: 'Try again', onClick: load }}
      />
    )
  }
  return react
}
