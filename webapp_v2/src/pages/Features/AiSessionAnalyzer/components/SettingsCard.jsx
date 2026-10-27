import { useState } from 'react'
import { Box, Collapse, Divider, Group, Paper, Stack, Text, ThemeIcon, UnstyledButton } from '@mantine/core'
import { Check, ChevronDown, ChevronUp } from 'lucide-react'

// A collapsible card under a risk level (AI policy settings, Approver,
// Hand-off): icon, title, subtitle and chevron in a header that is green while
// open. A closed card shows a green check when `done`. `badge` sits beside the
// title. The body is an inner bordered panel, or bare when `panel` is false
// (the AI policy settings rows carry their own borders).
export default function SettingsCard({ icon: Icon, title, subtitle, badge, done = true, panel = true, children }) {
  const [open, setOpen] = useState(true)
  return (
    <Paper withBorder radius="lg" style={{ overflow: 'hidden' }}>
      <UnstyledButton w="100%" p="md" bg={open ? 'green.0' : 'gray.0'} onClick={() => setOpen((o) => !o)} aria-expanded={open}>
        <Group wrap="nowrap" gap="md">
          <ThemeIcon size={40} radius="md" variant="light" color={open ? 'green' : 'gray'}>
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
          {!open && done ? (
            <ThemeIcon size={22} radius="xl" variant="light" color="green" aria-label="Configured">
              <Check size={12} aria-hidden="true" />
            </ThemeIcon>
          ) : (
            <Box w={22} h={22} bdrs="xl" bg="var(--mantine-color-body)" opacity={0.7} />
          )}
          {open ? <ChevronUp size={16} aria-hidden="true" /> : <ChevronDown size={16} aria-hidden="true" />}
        </Group>
      </UnstyledButton>
      <Collapse in={open}>
        <Divider />
        <Box p="md">
          {panel ? (
            <Paper withBorder radius="lg" p="lg">
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
