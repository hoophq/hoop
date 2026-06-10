import { Navigate } from 'react-router-dom'
import { useUserStore } from '@/stores/useUserStore'
import { MOBILE_ADMIN_FLAG } from './constants'

/**
 * Feature-flag gate for the Mobile Admin PWA. Renders children only when
 * `experimental.mobile_admin` is enabled for the org; otherwise sends the
 * user back to the desktop app.
 */
function MobileGate({ children }) {
  const isFeatureFlagEnabled = useUserStore((s) => s.isFeatureFlagEnabled)

  if (!isFeatureFlagEnabled(MOBILE_ADMIN_FLAG)) {
    return <Navigate to="/" replace />
  }

  return children
}

export default MobileGate
