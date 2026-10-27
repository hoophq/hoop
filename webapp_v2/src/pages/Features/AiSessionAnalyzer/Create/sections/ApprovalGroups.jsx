import { useState } from 'react'
import { Collapse, Divider, Group, Paper, ScrollArea, Stack, Text, UnstyledButton } from '@mantine/core'
import { ChevronDown, ChevronUp, Search } from 'lucide-react'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Checkbox from '@/components/Checkbox'
import TextInput from '@/components/TextInput'
import classes from '../Create.module.css'

// The groups whose members approve a level's hold. `options` holds every
// group of the org plus the level's stored ones; an empty `value` means the
// administrators approve.
export default function ApprovalGroups({ options, value, onChange }) {
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

  return (
    <Stack gap={6}>
      <Paper withBorder radius="lg" className={classes.clipped}>
        <UnstyledButton w="100%" px="md" py="sm" bg="gray.0" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
          <Group justify="space-between" wrap="nowrap" gap="md">
            <Stack gap={2}>
              <Text size="sm" fw={600}>
                Approval group
              </Text>
              <Text size="xs" c="dimmed">
                Select the groups whose members approve the request.
              </Text>
            </Stack>
            <Group gap="sm" wrap="nowrap">
              {all && (
                <Badge variant="light" color="green" tag chip>
                  All groups
                </Badge>
              )}
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
      <Text size="xs" c="dimmed">
        With no group selected, the administrators approve.
      </Text>
    </Stack>
  )
}
