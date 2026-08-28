import { Navigate, Outlet } from 'react-router-dom'
import { useUserStore } from '@/stores/useUserStore'

/**
 * Gates a group of routes on one licence feature. A layout route:
 *
 *   <Route element={<LicenseRoute feature="guardrails" />}>
 *     <Route path="/guardrails" element={<Guardrails />} />
 *   </Route>
 *
 * It is a **synchronous check and nothing else** — no effect, no fetch, no
 * loading state. That is the whole reason it exists separately from
 * ProtectedRoute rather than being another prop on it.
 *
 * ProtectedRoute bootstraps the session: it fetches the user, retries
 * /serverinfo and loads the feature flags, and shows the dark full-screen
 * AuthPageLoader while it does. Its guard against running twice is a ref, so it
 * is per instance. Nest a second one to carry a licence check and you get a
 * second bootstrap, a second round trip, and the auth screen flashing inside the
 * app shell every time someone enters the group. That shipped once; this is the
 * fix.
 *
 * Safe to be synchronous because nothing reaches here until the single
 * ProtectedRoute above has finished — it renders no children before then. And
 * `isLicenseFeatureEnabled` fails closed while /serverinfo is unknown, so a
 * failed bootstrap denies rather than leaks.
 */
export default function LicenseRoute({ feature }) {
  const isLicenseFeatureEnabled = useUserStore((s) => s.isLicenseFeatureEnabled)

  if (feature && !isLicenseFeatureEnabled(feature)) {
    return <Navigate to="/" replace />
  }

  return <Outlet />
}
