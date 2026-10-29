import { useMemo, useState } from 'react'
import { Combobox, Group, Pill, PillsInput, ScrollArea, useCombobox } from '@mantine/core'
import { Award, Plus } from 'lucide-react'
import classes from './AttributesSelect.module.css'

const CREATE_PREFIX = '$create:'

/**
 * Role attributes selector (Configure Role, resource setup wizard). Every
 * selected attribute is a removable pill. Options flagged `managed: true` (the
 * protection-profile attribute) render with the indigo award styling — in the
 * field and in the dropdown — but behave like any other attribute: removing the
 * pill detaches the role from the profile on save, and it can be re-added from
 * the dropdown, where managed entries are listed first.
 *
 * With `onCreate`, a search that matches no option offers to create it.
 */
export default function AttributesSelect({
  options = [],
  value = [],
  onChange,
  onCreate,
  placeholder,
}) {
  const combobox = useCombobox({
    onDropdownClose: () => combobox.resetSelectedOption(),
  })
  const [search, setSearch] = useState('')

  const selectedSet = useMemo(() => new Set(value), [value])
  const optionByValue = useMemo(
    () => new Map(options.map((o) => [o.value, o])),
    [options],
  )

  const handleOptionSubmit = (val) => {
    if (val.startsWith(CREATE_PREFIX)) {
      onCreate(val.slice(CREATE_PREFIX.length))
      setSearch('')
      return
    }
    const next = selectedSet.has(val) ? value.filter((v) => v !== val) : [...value, val]
    onChange?.(next)
    setSearch('')
  }

  const handleValueRemove = (val) => onChange?.(value.filter((v) => v !== val))

  const pills = value.map((val) => {
    const option = optionByValue.get(val)
    if (option?.managed) {
      return (
        <Pill
          key={val}
          withRemoveButton
          onRemove={() => handleValueRemove(val)}
          className={classes.managedPill}
          bg="indigo.3"
          c="indigo.9"
        >
          <Group gap={4} wrap="nowrap" component="span" display="inline-flex">
            <Award size={12} aria-hidden="true" />
            {option.label}
          </Group>
        </Pill>
      )
    }
    return (
      <Pill key={val} withRemoveButton onRemove={() => handleValueRemove(val)}>
        {option?.label ?? val}
      </Pill>
    )
  })

  const searchTerm = search.trim().toLowerCase()

  const visibleOptions = options
    .filter((o) => !selectedSet.has(o.value))
    .filter((o) => o.label.toLowerCase().includes(searchTerm))

  // Managed entries come first in the menu, with the award styling.
  const optionNodes = [
    ...visibleOptions.filter((o) => o.managed),
    ...visibleOptions.filter((o) => !o.managed),
  ].map((o) =>
    o.managed ? (
      <Combobox.Option value={o.value} key={o.value} className={classes.managedOption}>
        <Group gap={4} wrap="nowrap">
          <Award size={12} aria-hidden="true" />
          {o.label}
        </Group>
      </Combobox.Option>
    ) : (
      <Combobox.Option value={o.value} key={o.value}>
        {o.label}
      </Combobox.Option>
    ),
  )

  const typed = search.trim()
  const canCreate =
    Boolean(onCreate) &&
    typed !== '' &&
    !options.some((o) => o.value === typed) &&
    !selectedSet.has(typed)
  if (canCreate) {
    optionNodes.push(
      <Combobox.Option value={CREATE_PREFIX + typed} key={CREATE_PREFIX + typed}>
        <Group gap={4} wrap="nowrap">
          <Plus size={12} aria-hidden="true" />
          {`Create "${typed}"`}
        </Group>
      </Combobox.Option>,
    )
  }

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
            {pills}
            <Combobox.EventsTarget>
              <PillsInput.Field
                value={search}
                placeholder={value.length === 0 ? placeholder : ''}
                onFocus={() => combobox.openDropdown()}
                onChange={(event) => {
                  combobox.openDropdown()
                  setSearch(event.currentTarget.value)
                }}
                onKeyDown={(event) => {
                  // Backspace removes the last pill, managed or not.
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
