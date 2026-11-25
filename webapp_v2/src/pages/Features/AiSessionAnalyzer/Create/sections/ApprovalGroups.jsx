import { useState } from 'react'
import { Collapse, Divider, Group, Paper, ScrollArea, Stack, Text, UnstyledButton } from '@mantine/core'
import { ChevronDown, ChevronUp, Search } from 'lucide-react'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Checkbox from '@/components/Checkbox'
import TextInput from '@/components/TextInput'
import classes from '../Create.module.css'

// The two uses of the card. Static: the groups whose members approve, an
// empty list leaving it to the administrators. Candidates: the groups the AI
// picks from, every box checked meaning every group (ApprovalRouting maps
// that to the backend's empty list).
const VARIANTS = {
  static: {
    title: 'Approval group',
    subtitle: 'Select the groups whose members approve the request.',
    helper: 'With no group selected, the administrators approve.',
    count: false,
  },
  candidates: {
    title: 'Candidate Groups',
    subtitle: 'AI selects approvers from these groups.',
    helper: 'With no group selected, the AI selects from every group.',
    count: true,
  },
}

// `options` holds every group of the org plus the level's stored ones;
// `value` is the checked ones.
export default function ApprovalGroups({ options, value, onChange, variant = 'static' }) {
  const copy = VARIANTS[variant]
  const [open, setOpen] = useState(true)
  const [query, setQuery] = useState('')
  const selected = new Set(value)
  const needle = query.trim().toLowerCase()
  const shown = options.filter((group) => group.toLowerCase().includes(needle))
  const all = options.length > 0 && options.every((group) => selected.has(group))

  const toggle = (group) => {
    const next = new Set(selected)
    if (next.has(group)) next.delete(group)
    else next.add(group)
    onChange(options.filter((g) => next.has(g)))
  }

  // Candidates: every box checked, or none, both mean every group.
  let badge = null
  if (all || (copy.count && value.length === 0)) {
    badge = (
      <Badge variant="light" color="green" tag chip>
        All groups
      </Badge>
    )
  } else if (copy.count) {
    badge = (
      <Badge variant="light" color="indigo" tag chip>
        {`${value.length} of ${options.length} groups`}
      </Badge>
    )
  }

  return (
    <Stack gap={6}>
      <Paper withBorder className={classes.clipped}>
        <UnstyledButton w="100%" px="md" py="sm" bg="var(--hoop-surface-muted)" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
          <Group justify="space-between" wrap="nowrap" gap="md">
            <Stack gap={2}>
              <Text size="sm" fw={600}>
                {copy.title}
              </Text>
              <Text size="xs" c="dimmed">
                {copy.subtitle}
              </Text>
            </Stack>
            <Group gap="sm" wrap="nowrap">
              {badge}
              {open ? <ChevronUp size={16} aria-hidden="true" /> : <ChevronDown size={16} aria-hidden="true" />}
            </Group>
          </Group>
        </UnstyledButton>
        <Collapse in={open}>
          <Divider />
          <Stack gap="sm" p="md">
            <TextInput
              size="sm"
              placeholder="Search groups"
              aria-label="Search groups"
              leftSection={<Search size={14} aria-hidden="true" />}
              value={query}
              onChange={(e) => setQuery(e.currentTarget.value)}
            />
            <Group justify="space-between" wrap="nowrap">
              <Text size="xs" c="dimmed">
                {`${value.length} of ${options.length} selected`}
              </Text>
              <Group gap="md" wrap="nowrap">
                <Button variant="transparent" color="gray" size="xs" px={0} onClick={() => onChange(options)}>
                  Select All
                </Button>
                <Button variant="transparent" size="xs" px={0} onClick={() => onChange([])}>
                  Clear selection
                </Button>
              </Group>
            </Group>
            <ScrollArea.Autosize mah={150} type="auto" offsetScrollbars>
              <Stack gap="sm">
                {shown.map((group) => (
                  <Checkbox
                    key={group}
                    label={
                      <Text size="sm" fw={500}>
                        {group}
                      </Text>
                    }
                    checked={selected.has(group)}
                    onChange={() => toggle(group)}
                  />
                ))}
                {shown.length === 0 && (
                  <Text size="sm" c="dimmed">
                    {options.length === 0 ? 'No user groups defined yet.' : 'No group matches the search.'}
                  </Text>
                )}
              </Stack>
            </ScrollArea.Autosize>
          </Stack>
        </Collapse>
      </Paper>
      {(variant === 'static' || value.length === 0) && (
        <Text size="xs" c="dimmed">
          {copy.helper}
        </Text>
      )}
    </Stack>
  )
}
