import { useMemo, useState } from 'react'
import { Combobox, Group, Pill, PillsInput, ScrollArea, useCombobox } from '@mantine/core'
import { Award } from 'lucide-react'
import { labelForManagedAttribute } from '@/features/ProtectionProfiles/constants'
import classes from './AttributesSelect.module.css'

/**
 * Attributes selector for the Details tab. Renders the connection's
 * Hoop-managed attributes (e.g. the protection profile) as non-removable
 * indigo pills inside the field, alongside the user-editable attribute
 * pills. Managed attributes never appear as dropdown options and never
 * reach `onChange` — they are applied and removed by the gateway only.
 */
export default function AttributesSelect({
  options = [],
  value = [],
  onChange,
  managedAttributes = [],
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

  const handleValueToggle = (val) => {
    const next = selectedSet.has(val) ? value.filter((v) => v !== val) : [...value, val]
    onChange?.(next)
    setSearch('')
  }

  const handleValueRemove = (val) => onChange?.(value.filter((v) => v !== val))

  const managedPills = managedAttributes.map((name) => (
    <Pill key={name} className={classes.managedPill} radius="xl">
      <Group gap={4} wrap="nowrap" component="span" display="inline-flex">
        <Award size={12} aria-hidden="true" />
        {labelForManagedAttribute(name)}
      </Group>
    </Pill>
  ))

  const pills = value.map((val) => (
    <Pill key={val} withRemoveButton onRemove={() => handleValueRemove(val)}>
      {labelByValue.get(val) ?? val}
    </Pill>
  ))

  const optionNodes = options
    .filter((o) => !selectedSet.has(o.value))
    .filter((o) => o.label.toLowerCase().includes(search.trim().toLowerCase()))
    .map((o) => (
      <Combobox.Option value={o.value} key={o.value}>
        {o.label}
      </Combobox.Option>
    ))

  return (
    <Combobox store={combobox} onOptionSubmit={handleValueToggle}>
      <Combobox.DropdownTarget>
        <PillsInput onClick={() => combobox.openDropdown()}>
          <Pill.Group>
            {managedPills}
            {pills}
            <Combobox.EventsTarget>
              <PillsInput.Field
                value={search}
                placeholder={
                  value.length === 0 && managedAttributes.length === 0 ? placeholder : ''
                }
                onFocus={() => combobox.openDropdown()}
                onChange={(event) => {
                  combobox.openDropdown()
                  setSearch(event.currentTarget.value)
                }}
                onKeyDown={(event) => {
                  // Backspace only removes user pills — managed pills stay.
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
            {optionNodes.length === 0 ? (
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
