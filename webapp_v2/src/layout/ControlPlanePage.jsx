import ProtectedRoute from '@/components/ProtectedRoute'
import ControlPlaneLayout from './ControlPlaneLayout'
import PageLayout from './PageLayout'

// The shell of a React page in the control plane: the shared auth gate (no
// onboarding), the control plane Layout (which mounts its own palette), the
// padded body. `adminOnly`, `role` and `licenseFeature` go to the gate. Its
// sibling is GatewayPage.jsx.
export default function ControlPlanePage({ children, ...gate }) {
  return (
    <ProtectedRoute {...gate}>
      <ControlPlaneLayout>
        <PageLayout>{children}</PageLayout>
      </ControlPlaneLayout>
    </ProtectedRoute>
  )
}
