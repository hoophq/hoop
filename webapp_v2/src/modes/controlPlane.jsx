import { theme, cssVariablesResolver } from '@/theme'
import ProtectedRoute from '@/components/ProtectedRoute'
import ControlPlanePage from '@/layout/ControlPlanePage'
import Home from '@/pages/Home'
import NotFound from '@/pages/NotFound'

/**
 * The control plane product: an admin manages a fleet of sidecars, configures
 * features once for all of them and approves reviews.
 *
 * Every React route of Router.jsx exists here too; the sidebar
 * (layout/Sidebar/controlPlaneNav.js) says what the product shows. The only
 * things that do not exist are the ClojureScript leaves: '/' is the landing by
 * role, '/onboarding/*' and '/*' are a 404, and the CLJS bundle is never loaded.
 */
const notFound = (
  <ControlPlanePage>
    <NotFound />
  </ControlPlanePage>
)

export default {
  id: 'control-plane',
  theme: { theme, cssVariablesResolver },
  // '/' sends each role to its page and shows everyone else the dead end.
  postLoginPath: '/',
  postSetupPath: '/',
  Page: ControlPlanePage,
  Guard: ProtectedRoute,
  Home: (
    <ControlPlanePage>
      <Home />
    </ControlPlanePage>
  ),
  Onboarding: notFound,
  CatchAll: notFound,
}
