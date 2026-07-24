import { useMemo, useState } from 'react'
import { Combobox, Group, Pill, PillsInput, ScrollArea, useCombobox } from '@mantine/core'
import { Award } from 'lucide-react'
import classes from './AttributesSelect.module.css'

/**
 * Attributes selector for the Details tab. Mirrors the creation wizard:
 * the protection-profile attribute renders as a distinct indigo award pill
 * inside the field, is removable (detaching the connection from the
 * profile on save) and re-addable from the dropdown, where it also shows
 * with the award styling. Managed entries never reach `onChange` — they
 * flow through `onManagedChange` only.
 */
export default function AttributesSelect({
  options = [],
  value = [],
  onChange,
  managedOptions = [],
  managedValue = [],
  onManagedChange,
  placeholder,
}) {
  const combobox = useCombobox({
    onDropdownClose: () => combobox.resetSelectedOption(),
  })
  const [search, setSearch] = useState('')

  const selectedSet = useMemo(() => new Set(value), [value])
  const managedSet = useMemo(() => new Set(managedValue), [managedValue])
  const managedByValue = useMemo(
    () => new Map(managedOptions.map((o) => [o.value, o])),
    [managedOptions],
  )
  const labelByValue = useMemo(
    () => new Map(options.map((o) => [o.value, o.label])),
    [options],
  )

  const handleOptionSubmit = (val) => {
    if (managedByValue.has(val)) {
      onManagedChange?.([...managedValue, val])
    } else {
      const next = selectedSet.has(val) ? value.filter((v) => v !== val) : [...value, val]
      onChange?.(next)
    }
    setSearch('')
  }

  const handleValueRemove = (val) => onChange?.(value.filter((v) => v !== val))
  const handleManagedRemove = (val) =>
    onManagedChange?.(managedValue.filter((v) => v !== val))

  const managedPills = managedValue.map((val) => (
    <Pill
      key={val}
      withRemoveButton
      onRemove={() => handleManagedRemove(val)}
      className={classes.managedPill}
      radius="xl"
    >
      <Group gap={4} wrap="nowrap" component="span" display="inline-flex">
        <Award size={12} aria-hidden="true" />
        {managedByValue.get(val)?.label ?? val}
      </Group>
    </Pill>
  ))

  const pills = value.map((val) => (
    <Pill key={val} withRemoveButton onRemove={() => handleValueRemove(val)}>
      {labelByValue.get(val) ?? val}
    </Pill>
  ))

  const searchTerm = search.trim().toLowerCase()

  // Detached managed entries come first in the menu, with the award styling.
  const managedOptionNodes = managedOptions
    .filter((o) => !managedSet.has(o.value))
    .filter((o) => o.label.toLowerCase().includes(searchTerm))
    .map((o) => (
      <Combobox.Option value={o.value} key={o.value} className={classes.managedOption}>
        <Group gap={4} wrap="nowrap">
          <Award size={12} aria-hidden="true" />
          {o.label}
        </Group>
      </Combobox.Option>
    ))

  const optionNodes = options
    .filter((o) => !selectedSet.has(o.value))
    .filter((o) => o.label.toLowerCase().includes(searchTerm))
    .map((o) => (
      <Combobox.Option value={o.value} key={o.value}>
        {o.label}
      </Combobox.Option>
    ))

  const empty = managedOptionNodes.length === 0 && optionNodes.length === 0

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
                  value.length === 0 && managedValue.length === 0 ? placeholder : ''
                }
                onFocus={() => combobox.openDropdown()}
                onChange={(event) => {
                  combobox.openDropdown()
                  setSearch(event.currentTarget.value)
                }}
                onKeyDown={(event) => {
                  // Backspace removes the last user pill first, then the
                  // managed pill — same order they render in.
                  if (event.key === 'Backspace' && search.length === 0) {
                    if (value.length > 0) {
                      event.preventDefault()
                      handleValueRemove(value[value.length - 1])
                    } else if (managedValue.length > 0) {
                      event.preventDefault()
                      handleManagedRemove(managedValue[managedValue.length - 1])
                    }
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
              <>
                {managedOptionNodes}
                {optionNodes}
              </>
            )}
          </ScrollArea.Autosize>
        </Combobox.Options>
      </Combobox.Dropdown>
    </Combobox>
  )
}
