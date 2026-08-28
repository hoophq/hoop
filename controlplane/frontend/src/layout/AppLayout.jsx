import { Outlet } from 'react-router-dom'
import ProtectedRoute from '@/components/ProtectedRoute'
import Layout from './Layout'
import PageLayout from './PageLayout'

/**
 * The chrome every signed-in page shares: the auth and admin gate, the shell,
 * the page padding, and the command palette that PageLayout mounts.
 *
 * It is a **layout route**, not a wrapper a page opts into:
 *
 *   <Route element={<AppLayout />}>
 *     <Route path="/sidecars" element={<Sidecars />} />
 *   </Route>
 *
 * A page nested here cannot forget the shell, and a page that must not have it
 * — the `/` dead end, the auth routes — is visibly outside the group rather
 * than quietly missing a wrapper.
 *
 * Every signed-in route in this app is admin-only, so the check lives here once
 * instead of on each route. Licence gating is not uniform, so it is a separate
 * nested components/LicenseRoute per feature group — which is deliberately NOT
 * another ProtectedRoute, because a second one bootstraps the session again.
 *
 * There is a second reason to express this as one element rather than repeat it
 * per route. React Router reuses a component instance across sibling routes
 * whose element tree starts with the same component, and ProtectedRoute already
 * carries a workaround that depends on being reused (see its `redirectTo`
 * effect). Today that holds because every route happens to start with the same
 * wrapper. Here it holds because there is only one instance.
 */
export default function AppLayout() {
  return (
    <ProtectedRoute adminOnly>
      <Layout>
        <PageLayout>
          <Outlet />
        </PageLayout>
      </Layout>
    </ProtectedRoute>
  )
}
