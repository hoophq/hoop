import { useCallback, useEffect, useState } from 'react'
import { Navigate } from 'react-router-dom'
import PageLoader from '@/components/PageLoader'
import EmptyState from '@/layout/EmptyState'
import OnboardingLayout from '@/layout/OnboardingLayout'
import { connectionsService } from '@/services/connections'
import { useUserStore } from '@/stores/useUserStore'
import { onboardingPath } from './onboarding'

// /onboarding sends each user to the step they need.
export default function Onboarding() {
  const user = useUserStore((s) => s.user)
  const isAdmin = useUserStore((s) => s.isAdmin)
  const [hasConnections, setHasConnections] = useState(null)
  const [error, setError] = useState(null)

  const check = useCallback(
    () =>
      connectionsService
        .list({ page: 1, page_size: 1 })
        .then(({ data }) => setHasConnections((data?.data ?? []).length > 0))
        .catch((err) => setError(err.response?.data?.message ?? '')),
    [],
  )
  const retry = () => {
    setError(null)
    check()
  }

  useEffect(() => {
    if (isAdmin) check()
  }, [isAdmin, check])

  if (isAdmin && hasConnections === null) {
    if (error === null) return <PageLoader overlay />
    return (
      <OnboardingLayout>
        <EmptyState
          title="Could not load your Resources"
          description={error || undefined}
          action={{ label: 'Try again', onClick: retry }}
        />
      </OnboardingLayout>
    )
  }

  const path = onboardingPath({
    isAdmin,
    hasConnections,
    protectionProfile: user?.default_protection_profile,
  })
  return <Navigate to={path} replace />
}
