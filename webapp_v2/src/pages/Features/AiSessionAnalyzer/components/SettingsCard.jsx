import { Box, Collapse, Divider, Group, Paper, Stack, Text, ThemeIcon, UnstyledButton } from '@mantine/core'
import { Check, ChevronDown, ChevronUp } from 'lucide-react'
import classes from '../Create/Create.module.css'

// A collapsible card under a risk level (AI policy settings, Approver,
// Hand-off): icon, title, subtitle and chevron in a header that is green while
// open. The stack owns `open` (CardStack): one card of a level is open at a
// time. A closed card shows a green check when `done`, else a grey circle; an
// open one a white circle. `badge` sits beside the title. The body is an inner
// bordered panel, or bare when `panel` is false.
export default function SettingsCard({ icon: Icon, title, subtitle, badge, done, open, onToggle, panel = true, children }) {
  let status
  if (!open && done) {
    status = (
      <ThemeIcon size={22} variant="light" color="green" aria-label="Configured">
        <Check size={12} aria-hidden="true" />
      </ThemeIcon>
    )
  } else {
    status = <Box w={22} h={22} bdrs="xl" bg={open ? 'var(--mantine-color-body)' : 'var(--hoop-surface-strong)'} />
  }
  return (
    <Paper withBorder className={classes.clipped}>
      <UnstyledButton
        w="100%"
        p="md"
        bg={open ? 'var(--hoop-tint-green)' : 'var(--hoop-surface-muted)'}
        onClick={onToggle}
        aria-expanded={open}
      >
        <Group wrap="nowrap" gap="md">
          <ThemeIcon size={40} variant="light" color="gray">
            <Icon size={18} aria-hidden="true" />
          </ThemeIcon>
          <Stack gap={2} flex={1}>
            <Group gap="xs" wrap="nowrap">
              <Text size="sm" fw={600}>
                {title}
              </Text>
              {badge}
            </Group>
            <Text size="xs" c="dimmed">
              {subtitle}
            </Text>
          </Stack>
          {status}
          {open ? <ChevronUp size={16} aria-hidden="true" /> : <ChevronDown size={16} aria-hidden="true" />}
        </Group>
      </UnstyledButton>
      <Collapse in={open}>
        <Divider />
        <Box p="md">
          {panel ? (
            <Paper withBorder p="lg">
              {children}
            </Paper>
          ) : (
            children
          )}
        </Box>
      </Collapse>
    </Paper>
  )
}
