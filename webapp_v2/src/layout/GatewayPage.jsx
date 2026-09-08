import GatewayProtectedRoute from '@/components/GatewayProtectedRoute'
import GatewayLayout from './GatewayLayout'
import PageLayout from './PageLayout'
import GatewayCommandPalette from '@/features/CommandPalette/GatewayCommandPalette'

// The shell of a React page in the gateway: auth gate with the onboarding
// redirect, the gateway Layout, the padded body. The palette is mounted here, not
// in PageLayout, so it exists on every migrated page and never on the ClojureApp
// leaves (modes/gateway.jsx). `adminOnly`, `role` and `licenseFeature` go to the
// gate. Its sibling is ControlPlanePage.jsx.
export default function GatewayPage({ children, ...gate }) {
  return (
    <GatewayProtectedRoute {...gate}>
      <GatewayLayout>
        <PageLayout>
          {children}
          <GatewayCommandPalette />
        </PageLayout>
      </GatewayLayout>
    </GatewayProtectedRoute>
  )
}
