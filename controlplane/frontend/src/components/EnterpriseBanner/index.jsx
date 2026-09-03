import { Box, Group, Stack, Text } from '@mantine/core'
import AddLicenseCta from '@/components/AddLicenseCta'
import Button from '@/components/Button'
import { openSales } from '@/utils/support'
import classes from './EnterpriseBanner.module.css'

const DEFAULT_TITLE = 'Unlock all protection controls'
const DEFAULT_SUBTITLE =
  'The control plane is part of the Enterprise plan. Add your license to create rules, or talk to sales.'

/**
 * Dark enterprise upsell banner pinned to feature pages for free-plan users.
 * React counterpart of the CLJS activation-journey enterprise banner
 * (webapp/src/webapp/features/activation_journey/views/enterprise_banner.cljs)
 * so both stacks render the same visual.
 *
 * "Add license" opens the shared modal (components/AddLicenseCta). "Talk to
 * Sales" opens Intercom when analytics tracking is enabled, otherwise
 * hoop.dev/meet in a new tab. Always gate the render on
 * `useUserStore.isFreeLicense` at the call site.
 */
export default function EnterpriseBanner({
  title = DEFAULT_TITLE,
  subtitle = DEFAULT_SUBTITLE,
  badgeLabel = 'Enterprise',
}) {
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
        <Group gap="xs" wrap="nowrap">
          <Button variant="subtle" className={classes.secondary} onClick={() => openSales()}>
            Talk to Sales
          </Button>
          <AddLicenseCta className={classes.action} />
        </Group>
      </Group>
    </Box>
  )
}
