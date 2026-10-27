import { useState } from 'react'
import { Box, Collapse, Divider, Group, Paper, Stack, Text, ThemeIcon, UnstyledButton } from '@mantine/core'
import { Check, ChevronDown, ChevronUp } from 'lucide-react'

// A collapsible card under a risk level (Approver, Hand-off): icon, title,
// subtitle and chevron in a header that is green while open, and a body in an
// inner bordered panel. A closed card shows a green check.
export default function SettingsCard({ icon: Icon, title, subtitle, children }) {
  const [open, setOpen] = useState(true)
  return (
    <Paper withBorder radius="lg" style={{ overflow: 'hidden' }}>
      <UnstyledButton w="100%" p="md" bg={open ? 'green.0' : 'gray.0'} onClick={() => setOpen((o) => !o)} aria-expanded={open}>
        <Group wrap="nowrap" gap="md">
          <ThemeIcon size={40} radius="md" variant="light" color={open ? 'green' : 'gray'}>
            <Icon size={18} aria-hidden="true" />
          </ThemeIcon>
          <Stack gap={2} flex={1}>
            <Text size="sm" fw={600}>
              {title}
            </Text>
            <Text size="xs" c="dimmed">
              {subtitle}
            </Text>
          </Stack>
          {open ? (
            <Box w={22} h={22} bdrs="xl" bg="var(--mantine-color-body)" opacity={0.7} />
          ) : (
            <ThemeIcon size={22} radius="xl" variant="light" color="green">
              <Check size={12} aria-hidden="true" />
            </ThemeIcon>
          )}
          {open ? <ChevronUp size={16} aria-hidden="true" /> : <ChevronDown size={16} aria-hidden="true" />}
        </Group>
      </UnstyledButton>
      <Collapse in={open}>
        <Divider />
        <Box p="md">
          <Paper withBorder radius="lg" p="lg">
            {children}
          </Paper>
        </Box>
      </Collapse>
    </Paper>
  )
}
