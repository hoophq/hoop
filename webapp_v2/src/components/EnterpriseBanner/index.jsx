import { Box, Group, Stack, Text } from '@mantine/core'
import Button from '@/components/Button'
import { useUserStore } from '@/stores/useUserStore'
import classes from './EnterpriseBanner.module.css'

const SALES_URL = 'https://hoop.dev/meet'
const INTERCOM_MESSAGE = 'I want to upgrade my current plan'

const DEFAULT_TITLE = 'Unlock all protection controls'
const DEFAULT_SUBTITLE =
  'Unlock unlimited Guardrails, Masking Rules, AI Session Analyzer, and more.'

/**
 * Dark enterprise upsell banner pinned to feature pages for free-plan users.
 * React counterpart of the CLJS activation-journey enterprise banner
 * (webapp/src/webapp/features/activation_journey/views/enterprise_banner.cljs)
 * so both stacks render the same visual.
 *
 * The "Talk to Sales" action mirrors FreeLicenseCallout: it opens Intercom
 * when analytics tracking is enabled, otherwise hoop.dev/meet in a new tab.
 * Always gate the render on `useUserStore.isFreeLicense` at the call site.
 */
export default function EnterpriseBanner({
  title = DEFAULT_TITLE,
  subtitle = DEFAULT_SUBTITLE,
  badgeLabel = 'Enterprise',
}) {
  const showIntercomMessage = useUserStore((state) => state.showIntercomMessage)

  const handleTalkToSales = () => {
    if (showIntercomMessage(INTERCOM_MESSAGE)) return
    window.open(SALES_URL, '_blank', 'noopener,noreferrer')
  }

  return (
    <Box className={classes.root}>
      <Group justify="space-between" align="center" gap="md" wrap="nowrap">
        <Stack gap="xsAlt">
          <Group gap="xs" align="center">
            <Text size="sm" fw={700} className={classes.title} component="span">
              {title}
            </Text>
            <span className={classes.badge}>{badgeLabel}</span>
          </Group>
          <Text size="xs" className={classes.subtitle}>
            {subtitle}
          </Text>
        </Stack>
        <Button size="sm" className={classes.action} onClick={handleTalkToSales}>
          Talk to Sales
        </Button>
      </Group>
    </Box>
  )
}
