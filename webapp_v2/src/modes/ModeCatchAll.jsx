import ClojureApp from '@/components/ClojureApp'
import PageLayout from '@/layout/PageLayout'
import NotFound from '@/pages/NotFound'
import { useModeConfig } from '@/modes'

// The leaf of the /* route. Router.jsx keeps <ProtectedRoute><Layout> around it
// so those instances are reused across navigations, as they always were.
export default function ModeCatchAll() {
  const { catchAll } = useModeConfig()
  if (catchAll === 'cljs') return <ClojureApp />
  return (
    <PageLayout>
      <NotFound />
    </PageLayout>
  )
}
