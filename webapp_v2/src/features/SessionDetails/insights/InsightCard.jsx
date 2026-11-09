import { useState } from 'react'
import { Collapse, Divider, Group, Paper, Stack, UnstyledButton } from '@mantine/core'
import { ChevronDown } from 'lucide-react'
import classes from './insights.module.css'

// A block of an insight card under a line.
export function CardSection({ children }) {
  return (
    <Stack gap="sm">
      <Divider />
      {children}
    </Stack>
  )
}

// `title` sits on the left and `summary` on the right while the card is closed
// (or `summary(open)` in both states).
// `aside` stays visible either way and may hold controls (the RDP analysis
// retry), so it sits outside the toggle button. `variant` is a Paper surface:
// subtle (outlined) or accent (AI Data Masking).
export default function InsightCard({ title, summary, aside, variant = 'subtle', children }) {
  const [open, setOpen] = useState(false)
  const toggle = () => setOpen((v) => !v)
  return (
    <Paper variant={variant} withBorder={variant === 'subtle'} radius="md" p="sm">
      <Group wrap="nowrap" gap="md">
        <UnstyledButton className={classes.header} flex={1} onClick={toggle} aria-expanded={open}>
          <Group justify="space-between" wrap="nowrap" gap="md">
            {title}
            {typeof summary === 'function' ? summary(open) : !open && summary}
          </Group>
        </UnstyledButton>
        {aside}
        <UnstyledButton onClick={toggle} tabIndex={-1} aria-hidden>
          <ChevronDown size={16} className={classes.chevron} data-open={open || undefined} />
        </UnstyledButton>
      </Group>
      <Collapse in={open}>
        <Stack gap="md" pt="sm">
          {children}
        </Stack>
      </Collapse>
    </Paper>
  )
}
