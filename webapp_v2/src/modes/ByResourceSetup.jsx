import { useEffect } from 'react'
import { Navigate, useSearchParams } from 'react-router-dom'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import { useConnectionsMetadataStore } from '@/stores/useConnectionsMetadataStore'
import { isReactSetup, setupConfigFor } from '@/pages/Resources/Setup/catalog'

/**
 * Picks the React or the CLJS resource setup wizard for /resources/new.
 * Used in Router.jsx only:
 *
 *   <ByResourceSetup react={<Page adminOnly><ResourceSetup /></Page>} cljs={CatchAll} />
 *
 * `?type=` is a catalog id. A subtype in REACT_SETUP_SUBTYPES renders React;
 * any other known id renders the CLJS wizard. No id, or an unknown one, goes
 * back to the catalog.
 */
export default function ByResourceSetup({ react, cljs }) {
  const [searchParams] = useSearchParams()
  const catalogId = searchParams.get('type')
  const metadata = useConnectionsMetadataStore((s) => s.metadata)
  const error = useConnectionsMetadataStore((s) => s.error)
  const load = useConnectionsMetadataStore((s) => s.load)

  useEffect(() => {
    if (catalogId) load()
  }, [catalogId, load])

  if (!catalogId) return <Navigate to="/resource-catalog" replace />
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
  const config = setupConfigFor(catalogId, metadata.connections)
  if (!config) return <Navigate to="/resource-catalog" replace />
  return isReactSetup(config) ? react : cljs
}
