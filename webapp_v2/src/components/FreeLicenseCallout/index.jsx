import { Anchor, Group, Text } from '@mantine/core'
import { AlertCircle, Info } from 'lucide-react'
import Alert from '@/components/Alert'
import { useUserStore } from '@/stores/useUserStore'

const SALES_URL = 'https://hoop.dev/meet'
const INTERCOM_MESSAGE = 'I want to upgrade my current plan'

/**
 * Free-license callout shown on gated feature pages.
 *
 * Props:
 * - message:  Body copy describing the limit for this feature.
 * - variant:  'info' (default — blue exploration callout) or 'limit' (red wall).
 *
 * Mirrors `webapp.shared-ui.free-license-banner` from the legacy CLJS app:
 * if analytics tracking is enabled, opens Intercom; otherwise opens the sales
 * page in a new tab.
 */
export default function FreeLicenseCallout({ message, variant = 'info' }) {
  const showIntercomMessage = useUserStore((state) => state.showIntercomMessage)
  const limit = variant === 'limit'
  const color = limit ? 'red' : 'blue'
  const Icon = limit ? AlertCircle : Info

  const handleClick = (event) => {
    event.preventDefault()
    if (showIntercomMessage(INTERCOM_MESSAGE)) return
    window.open(SALES_URL, '_blank', 'noopener,noreferrer')
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
        <Anchor
          href={SALES_URL}
          onClick={handleClick}
          c={color}
          fw={500}
          size="sm"
        >
          {'Contact our Sales team ↗'}
        </Anchor>
      </Group>
    </Alert>
  )
}
