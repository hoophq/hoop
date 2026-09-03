import { useState } from 'react'
import { Anchor, Group, Text } from '@mantine/core'
import { Link, useLocation } from 'react-router-dom'
import { TriangleAlert } from 'lucide-react'
import Alert from '@/components/Alert'
import { RENEW_MESSAGE } from '@/features/License/constants'
import { useUserStore } from '@/stores/useUserStore'
import { LICENSE_STATUS, daysUntilExpiration, formatLicenseDate } from '@/utils/license'
import { openSupport } from '@/utils/support'

const WARN_DAYS = 30
// Dismissing silences one step only; inside LOCKED_DAYS there is no close button.
const DISMISS_STEPS = [30, 14]
const LOCKED_DAYS = 7
const DISMISS_KEY = 'license-expiration-dismissed-for'

// Admin-only: renewing is their task.
function licenseNotice({ licenseInfo, isAdmin, userId, dismissedFor }) {
  if (!isAdmin || !licenseInfo) return null
  const { status, type, expire_at: expireAt, verify_error: verifyError } = licenseInfo

  if (status === LICENSE_STATUS.EXPIRED) {
    return {
      color: 'red',
      message: `Your hoop license expired on ${formatLicenseDate(expireAt)}. New sessions are blocked until a valid license is installed.`,
      action: 'update',
    }
  }

  if (status === LICENSE_STATUS.INVALID) {
    const reason = verifyError ? `: ${verifyError}` : ''
    return {
      color: 'red',
      message: `Your hoop license could not be verified${reason}. New sessions are blocked until a valid license is installed.`,
      action: 'update',
    }
  }

  if (type !== 'enterprise') return null

  const daysLeft = daysUntilExpiration(expireAt)
  if (daysLeft === null || daysLeft > WARN_DAYS) return null

  // Keyed by expiration so renewing re-arms it, and by user so one admin cannot
  // silence another.
  const step = daysLeft > LOCKED_DAYS ? DISMISS_STEPS.findLast((d) => daysLeft <= d) : null
  const dismissKey = step ? `${userId}:${expireAt}:${step}` : null
  if (dismissKey && dismissedFor === dismissKey) return null

  const when = daysLeft <= 0 ? 'today' : daysLeft === 1 ? 'tomorrow' : `in ${daysLeft} days`
  return {
    color: 'amber',
    message: `Your hoop license expires ${when}, on ${formatLicenseDate(expireAt)}. Renew it to keep sessions running.`,
    action: 'support',
    dismissKey,
  }
}

// Sits at the top of the shell content, so it reaches every page using the chrome.
export default function LicenseBanner() {
  const licenseInfo = useUserStore((state) => state.licenseInfo)
  const isAdmin = useUserStore((state) => state.isAdmin)
  const userId = useUserStore((state) => state.user?.id)
  const { pathname } = useLocation()
  const [dismissedFor, setDismissedFor] = useState(() => localStorage.getItem(DISMISS_KEY))

  const notice = licenseNotice({ licenseInfo, isAdmin, userId, dismissedFor })
  // The license page states the same thing next to the control that fixes it.
  // A literal path, not a constant: scripts/check-routes.mjs only sees literals.
  if (!notice || pathname === '/settings/license') return null

  const dismiss = () => {
    localStorage.setItem(DISMISS_KEY, notice.dismissKey)
    setDismissedFor(notice.dismissKey)
  }

  return (
    <Alert
      color={notice.color}
      variant="light"
      radius={0}
      py="sm"
      icon={<TriangleAlert size={18} />}
      withCloseButton={!!notice.dismissKey}
      onClose={dismiss}
      closeButtonLabel="Dismiss license warning"
    >
      <Group gap="xs" align="center" wrap="wrap">
        <Text size="sm" component="span">
          {notice.message}
        </Text>
        {notice.action === 'update' && (
          <Anchor component={Link} to="/settings/license" c={notice.color} fw={500} size="sm">
            Update license
          </Anchor>
        )}
        <Anchor
          component="button"
          type="button"
          onClick={() => openSupport(RENEW_MESSAGE)}
          c={notice.color}
          fw={500}
          size="sm"
        >
          {'Contact support ↗'}
        </Anchor>
      </Group>
    </Alert>
  )
}
