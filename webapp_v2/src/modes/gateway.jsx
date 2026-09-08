import { theme, cssVariablesResolver } from '@/theme'
import GatewayProtectedRoute from '@/components/GatewayProtectedRoute'
import ClojureApp from '@/components/ClojureApp'
import GatewayLayout from '@/layout/GatewayLayout'
import GatewayPage from '@/layout/GatewayPage'

/**
 * The gateway product: the app as it always was.
 *
 * `Page` is the shell of a React page (layout/GatewayPage). `Guard` is a React
 * route without the shell (onboarding). `Home`, `Onboarding` and `CatchAll` are
 * the three leaves that differ between the products; in the gateway all three
 * are ClojureScript, and this file is the only place that mounts ClojureApp.
 */
const cljs = (
  <GatewayProtectedRoute>
    <GatewayLayout>
      <ClojureApp />
    </GatewayLayout>
  </GatewayProtectedRoute>
)

export default {
  id: 'gateway',
  theme: { theme, cssVariablesResolver },
  // The web terminal is the landing page of a signed-in user. A new local org
  // continues in the CLJS onboarding.
  postLoginPath: '/client',
  postSetupPath: '/onboarding/setup',
  Page: GatewayPage,
  Guard: GatewayProtectedRoute,
  Home: cljs,
  // Onboarding renders without the shell (mirrors :auth layout in the legacy app).
  Onboarding: (
    <GatewayProtectedRoute>
      <ClojureApp />
    </GatewayProtectedRoute>
  ),
  CatchAll: cljs,
}
