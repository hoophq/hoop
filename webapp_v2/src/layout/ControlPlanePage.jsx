import ControlPlaneProtectedRoute from '@/components/ControlPlaneProtectedRoute'
import ControlPlaneLayout from './ControlPlaneLayout'
import PageLayout from './PageLayout'

// The shell of a React page in the control plane: the control plane gate (the
// shared auth gate plus the first-access license screen), the control plane
// Layout (which mounts its own palette), the padded body. `adminOnly`, `role` and `licenseFeature` go to the gate. Its
// sibling is GatewayPage.jsx.
export default function ControlPlanePage({ children, ...gate }) {
  return (
    <ControlPlaneProtectedRoute {...gate}>
      <ControlPlaneLayout>
        <PageLayout>{children}</PageLayout>
      </ControlPlaneLayout>
    </ControlPlaneProtectedRoute>
  )
}
