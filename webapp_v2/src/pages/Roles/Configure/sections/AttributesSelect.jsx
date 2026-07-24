import { useMemo, useState } from 'react'
import { Combobox, Group, Pill, PillsInput, ScrollArea, useCombobox } from '@mantine/core'
import { Award } from 'lucide-react'
import classes from './AttributesSelect.module.css'

/**
 * Attributes selector for the Details tab. Hoop-managed attributes (the
 * protection-profile attribute) render as read-only indigo award pills
 * inside the field — the API manages the association, so they cannot be
 * added or removed here. User attributes are regular removable pills.
 */
export default function AttributesSelect({
  options = [],
  value = [],
  onChange,
  managedOptions = [],
  placeholder,
}) {
  const combobox = useCombobox({
    onDropdownClose: () => combobox.resetSelectedOption(),
  })
  const [search, setSearch] = useState('')

  const selectedSet = useMemo(() => new Set(value), [value])
  const labelByValue = useMemo(
    () => new Map(options.map((o) => [o.value, o.label])),
    [options],
  )

  const handleOptionSubmit = (val) => {
    const next = selectedSet.has(val) ? value.filter((v) => v !== val) : [...value, val]
    onChange?.(next)
    setSearch('')
  }

  const handleValueRemove = (val) => onChange?.(value.filter((v) => v !== val))

  const managedPills = managedOptions.map((o) => (
    <Pill key={o.value} className={classes.managedPill} bg="indigo.3" c="indigo.9">
      <Group gap={4} wrap="nowrap" component="span" display="inline-flex">
        <Award size={12} aria-hidden="true" />
        {o.label}
      </Group>
    </Pill>
  ))

  const pills = value.map((val) => (
    <Pill key={val} withRemoveButton onRemove={() => handleValueRemove(val)}>
      {labelByValue.get(val) ?? val}
    </Pill>
  ))

  const searchTerm = search.trim().toLowerCase()

  const optionNodes = options
    .filter((o) => !selectedSet.has(o.value))
    .filter((o) => o.label.toLowerCase().includes(searchTerm))
    .map((o) => (
      <Combobox.Option value={o.value} key={o.value}>
        {o.label}
      </Combobox.Option>
    ))

  const empty = optionNodes.length === 0

  return (
    <Combobox store={combobox} onOptionSubmit={handleOptionSubmit}>
      <Combobox.DropdownTarget>
        <PillsInput
          onClick={() => combobox.openDropdown()}
          rightSection={<Combobox.Chevron />}
          rightSectionPointerEvents="none"
        >
          <Pill.Group>
            {managedPills}
            {pills}
            <Combobox.EventsTarget>
              <PillsInput.Field
                value={search}
                placeholder={
                  value.length === 0 && managedOptions.length === 0 ? placeholder : ''
                }
                onFocus={() => combobox.openDropdown()}
                onChange={(event) => {
                  combobox.openDropdown()
                  setSearch(event.currentTarget.value)
                }}
                onKeyDown={(event) => {
                  // Backspace removes the last user pill; managed pills are
                  // read-only and never removed.
                  if (event.key === 'Backspace' && search.length === 0 && value.length > 0) {
                    event.preventDefault()
                    handleValueRemove(value[value.length - 1])
                  }
                }}
              />
            </Combobox.EventsTarget>
          </Pill.Group>
        </PillsInput>
      </Combobox.DropdownTarget>

      <Combobox.Dropdown>
        <Combobox.Options>
          <ScrollArea.Autosize mah={240} type="auto">
            {empty ? (
              <Combobox.Empty>
                No attributes found. Go to Settings → Attributes to add one.
              </Combobox.Empty>
            ) : (
              optionNodes
            )}
          </ScrollArea.Autosize>
        </Combobox.Options>
      </Combobox.Dropdown>
    </Combobox>
  )
}
