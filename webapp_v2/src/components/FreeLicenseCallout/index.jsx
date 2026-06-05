import { Anchor, Group, Text } from '@mantine/core'
import { AlertCircle, Info } from 'lucide-react'
import Alert from '@/components/Alert'
import { useUserStore } from '@/stores/useUserStore'

const SALES_URL = 'https://hoop.dev/meet'

/**
 * Free-license callout shown on gated feature pages.
 *
 * Props:
 * - message:  Body copy describing the limit for this feature.
 * - variant:  'info' (default — blue exploration callout) or 'limit' (red wall).
 *
 * Mirrors `webapp.shared-ui.free-license-banner` from the legacy CLJS app. The
 * "Contact Sales" action delegates to the store's `requestDemo`, which opens
 * Intercom (booting it on demand) when analytics tracking is enabled, otherwise
 * falls back to the sales page.
 */
export default function FreeLicenseCallout({ message, variant = 'info' }) {
  const requestDemo = useUserStore((state) => state.requestDemo)
  const limit = variant === 'limit'
  const color = limit ? 'red' : 'blue'
  const Icon = limit ? AlertCircle : Info

  const handleClick = (event) => {
    event.preventDefault()
    requestDemo()
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
