import { useMemo, useState } from 'react'
import { Divider, Group, Popover, ScrollArea, Stack, Text, UnstyledButton } from '@mantine/core'
import { Check, ChevronLeft, ChevronRight, Search, Tag } from 'lucide-react'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import TextInput from '@/components/TextInput'
import { countSelected, displayKey, groupTags, tagKeyName } from '@/pages/Resources/utils'
import classes from './TagsFilter.module.css'

// Port of webapp/src/webapp/connections/views/tag_selector.cljs: pick values
// per tag key; `selected` is { key: [values] }.

function Option({ selected, onClick, children }) {
  return (
    <UnstyledButton
      className={classes.option}
      role="option"
      aria-selected={selected ?? undefined}
      onClick={onClick}
    >
      {children}
    </UnstyledButton>
  )
}

function ValuesView({ tagKey, values, selected, onChange, onBack }) {
  const current = selected[tagKey] ?? []
  const toggle = (value) => {
    const next = current.includes(value) ? current.filter((v) => v !== value) : [...current, value]
    const { [tagKey]: _omit, ...rest } = selected
    onChange(next.length ? { ...rest, [tagKey]: next } : rest)
  }
  const clear = () => {
    const { [tagKey]: _omit, ...rest } = selected
    onChange(rest)
  }
  return (
    <Stack gap="xs">
      <Group justify="space-between">
        <Button variant="subtle" color="gray" size="compact-sm" leftSection={<ChevronLeft size={16} />} onClick={onBack}>
          Back
        </Button>
        <Button variant="subtle" color="gray" size="compact-sm" onClick={clear}>
          Clear
        </Button>
      </Group>
      <Divider />
      <Text size="xs" c="dimmed">{displayKey(tagKey)}</Text>
      <ScrollArea.Autosize mah={256}>
        <Stack gap={2} role="listbox" aria-label={'Values for ' + displayKey(tagKey)}>
          <Option onClick={() => onChange({ ...selected, [tagKey]: values })}>
            <Text size="sm">Select all</Text>
          </Option>
          {values.map((value) => (
            <Option key={value} selected={current.includes(value)} onClick={() => toggle(value)}>
              <Text size="sm">{value}</Text>
              {current.includes(value) && <Check size={16} />}
            </Option>
          ))}
        </Stack>
      </ScrollArea.Autosize>
    </Stack>
  )
}

export default function TagsFilter({ tags, selected, onChange }) {
  const [opened, setOpened] = useState(false)
  const [search, setSearch] = useState('')
  const [currentKey, setCurrentKey] = useState(null)
  const grouped = useMemo(() => groupTags(tags), [tags])
  const count = countSelected(selected)
  const term = search.trim().toLowerCase()

  const searchResults = useMemo(() => {
    if (!term) return []
    return Object.entries(grouped).flatMap(([key, values]) =>
      values
        .filter((value) => tagKeyName(key).toLowerCase().includes(term) || value.toLowerCase().includes(term))
        .map((value) => ({ key, value })),
    )
  }, [grouped, term])

  const openKey = (key) => {
    setCurrentKey(key)
    setSearch('')
  }

  const close = () => {
    setOpened(false)
    setCurrentKey(null)
    setSearch('')
  }

  return (
    <Popover opened={opened} onChange={(next) => (next ? setOpened(true) : close())} position="bottom-start" width={300} withinPortal>
      <Popover.Target>
        <Button
          variant="field"
          data-filled={count > 0 || undefined}
          leftSection={<Tag size={16} aria-hidden="true" />}
          rightSection={count ? <Badge size="sm" circle color="gray.8" variant="filled">{count}</Badge> : null}
          aria-label={count ? 'Filter by tags, ' + count + ' selected' : 'Filter by tags'}
          onClick={() => (opened ? close() : setOpened(true))}
        >
          Tags
        </Button>
      </Popover.Target>
      <Popover.Dropdown p="xs" aria-label="Tag filter options">
        {currentKey ? (
          <ValuesView
            tagKey={currentKey}
            values={grouped[currentKey] ?? []}
            selected={selected}
            onChange={onChange}
            onBack={() => setCurrentKey(null)}
          />
        ) : (
          <Stack gap="xs">
            <TextInput
              placeholder="Search Tags"
              aria-label="Search Tags"
              value={search}
              onChange={(e) => setSearch(e.currentTarget.value)}
              leftSection={<Search size={14} />}
            />
            <ScrollArea.Autosize mah={256}>
              {term ? (
                <Stack gap={2} role="listbox" aria-label="Search results">
                  {searchResults.map(({ key, value }) => (
                    <Option key={key + '-' + value} onClick={() => openKey(key)}>
                      <Stack gap={0}>
                        <Text size="sm">{value}</Text>
                        <Text size="xs" c="dimmed">{'Value | ' + displayKey(key)}</Text>
                      </Stack>
                      <ChevronRight size={16} />
                    </Option>
                  ))}
                </Stack>
              ) : (
                <Stack gap={2} role="listbox" aria-label="Tag groups">
                  {Object.keys(grouped).map((key) => {
                    const selectedCount = (selected[key] ?? []).length
                    return (
                      <Option key={key} onClick={() => openKey(key)}>
                        <Text size="sm" truncate>{displayKey(key)}</Text>
                        <Group gap="xs" wrap="nowrap">
                          {selectedCount > 0 && <Badge size="sm" circle color="gray.8" variant="filled">{selectedCount}</Badge>}
                          <ChevronRight size={16} />
                        </Group>
                      </Option>
                    )
                  })}
                </Stack>
              )}
            </ScrollArea.Autosize>
          </Stack>
        )}
      </Popover.Dropdown>
    </Popover>
  )
}
