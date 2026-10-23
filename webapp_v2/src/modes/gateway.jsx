import { Navigate } from 'react-router-dom'
import GatewayProtectedRoute from '@/components/GatewayProtectedRoute'
import ClojureApp from '@/components/ClojureApp'
import GatewayLayout from '@/layout/GatewayLayout'
import GatewayPage from '@/layout/GatewayPage'

/**
 * The route wrappers Router.jsx reads.
 *
 * `Page` is the shell of a React page (layout/GatewayPage). `Guard` is a React
 * route without the shell (onboarding). `Home`, `Onboarding` and `CatchAll` are
 * the ClojureScript leaves; this file is the only place that mounts ClojureApp.
 */
const cljs = (
  <GatewayProtectedRoute>
    <GatewayLayout>
      <ClojureApp />
    </GatewayLayout>
  </GatewayProtectedRoute>
)

export default {
  Page: GatewayPage,
  Guard: GatewayProtectedRoute,
  // '/' is the terminal; the CLJS home panel, which sends an admin without
  // connections to onboarding, never renders under the shell.
  Home: (
    <GatewayProtectedRoute>
      <Navigate to="/client" replace />
    </GatewayProtectedRoute>
  ),
  // Onboarding renders without the shell (mirrors :auth layout in the legacy app).
  Onboarding: (
    <GatewayProtectedRoute>
      <ClojureApp />
    </GatewayProtectedRoute>
  ),
  CatchAll: cljs,
}
