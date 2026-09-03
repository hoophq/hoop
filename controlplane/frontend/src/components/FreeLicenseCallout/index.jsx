import { Anchor, Group, Text } from '@mantine/core'
import { AlertCircle, Info } from 'lucide-react'
import AddLicenseCta from '@/components/AddLicenseCta'
import Alert from '@/components/Alert'
import { RENEW_MESSAGE } from '@/features/License/constants'
import { useUserStore } from '@/stores/useUserStore'
import { needsLicenseRenewal } from '@/utils/license'
import { openSales, openSupport } from '@/utils/support'

/**
 * License callout shown on gated feature pages.
 *
 * Props:
 * - message:  Body copy describing what the license unlocks or blocks.
 * - variant:  'info' (blue) or 'limit' (red). When omitted it follows the
 *             license state: red for an expired or unverifiable license the
 *             organization already holds, blue for the free tier.
 *
 * Actions: "Add license" / "Update license" opens the shared modal. The second
 * link is "Talk to sales" on the free tier and "Contact support" for a customer
 * whose license needs renewal.
 */
export default function FreeLicenseCallout({ message, variant }) {
  const licenseInfo = useUserStore((state) => state.licenseInfo)
  const renewal = needsLicenseRenewal(licenseInfo)
  const limit = (variant ?? (renewal ? 'limit' : 'info')) === 'limit'
  const color = limit ? 'red' : 'blue'
  const Icon = limit ? AlertCircle : Info

  const handleSecondary = (event) => {
    event.preventDefault()
    if (renewal) openSupport(RENEW_MESSAGE)
    else openSales()
  }

  return (
    <Alert
      color={color}
      variant="light"
      icon={<Icon size={16} />}
      radius="md"
    >
      <Group gap="xs" align="center" wrap="wrap">
        <Text size="sm" component="span">
          {message}
        </Text>
        <AddLicenseCta variant="anchor" c={color} />
        <Anchor
          component="button"
          type="button"
          onClick={handleSecondary}
          c={color}
          fw={500}
          size="sm"
        >
          {renewal ? 'Contact support ↗' : 'Talk to sales ↗'}
        </Anchor>
      </Group>
    </Alert>
  )
}
