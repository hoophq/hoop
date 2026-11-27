import { useEffect } from 'react'
import AuthPageLoader from '@/components/AuthPageLoader'
import { authService } from '@/services/auth'
import { postLogoutPath, useAuthStore } from '@/stores/useAuthStore'

// /logout: CLJS still sends a session here (:logout-hoop), and so does signOut().
export default function Logout() {
  useEffect(() => {
    useAuthStore.getState().logout()
    authService
      .getPublicServerInfo()
      .then((info) => info?.auth_method)
      .catch(() => null)
      .then((method) => window.location.replace(postLogoutPath(method)))
  }, [])

  return <AuthPageLoader message="Logging out" description="You will be redirected in a few seconds." />
}
