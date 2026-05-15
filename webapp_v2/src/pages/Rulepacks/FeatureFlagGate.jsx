import { useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import PageLoader from '@/components/PageLoader'

const REDIRECT_DELAY_MS = 800

export default function FeatureFlagGate({ enabled, children }) {
  const navigate = useNavigate()

  useEffect(() => {
    if (!enabled) {
      const timer = setTimeout(() => navigate('/', { replace: true }), REDIRECT_DELAY_MS)
      return () => clearTimeout(timer)
    }
  }, [enabled, navigate])

  if (!enabled) {
    return <PageLoader h={400} />
  }

  return children
}
