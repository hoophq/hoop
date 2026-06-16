import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Box,
  Combobox,
  Group,
  Loader,
  Pill,
  PillsInput,
  Skeleton,
  Text,
  useCombobox,
} from '@mantine/core'
import { useIntersection } from '@mantine/hooks'
import { Check } from 'lucide-react'
import classes from './PaginatedMultiSelect.module.css'

/**
 * Multi-select over a paginated, server-searched option source (infinite scroll).
 * Controlled — the caller owns fetching; `selectedOptions` supplies labels for
 * values off the current page (a `null` label renders a loading skeleton).
 *
 * Usage:
 *   <PaginatedMultiSelect
 *     value={ids} onChange={setIds}
 *     options={options} selectedOptions={selectedOptions}
 *     loading={loading} hasMore={hasMore} onLoadMore={loadMore}
 *     searchValue={search} onSearchChange={setSearch}
 *   />
 */
export default function PaginatedMultiSelect({
  label,
  placeholder,
  required = false,
  disabled = false,
  value = [],
  onChange,
  options = [],
  selectedOptions = [],
  loading = false,
  hasMore = false,
  onLoadMore,
  searchValue = '',
  onSearchChange,
  onDropdownOpen,
}) {
  const combobox = useCombobox({
    onDropdownOpen: () => onDropdownOpen?.(),
    onDropdownClose: () => combobox.resetSelectedOption(),
  })

  // Capture the scroll container as state (not a ref) so useIntersection re-runs
  // with a real `root` once the element mounts.
  const [viewport, setViewport] = useState(null)
  const { ref: sentinelRef, entry } = useIntersection({
    root: viewport,
    rootMargin: '200px',
  })

  // Load one page per sentinel entry (rising edge only). Firing on every render
  // where it stays intersecting would load several pages at once.
  const wasIntersecting = useRef(false)
  useEffect(() => {
    const isIntersecting = entry?.isIntersecting ?? false
    if (isIntersecting && !wasIntersecting.current && hasMore && !loading) {
      onLoadMore?.()
    }
    wasIntersecting.current = isIntersecting
  }, [entry?.isIntersecting, hasMore, loading, onLoadMore])

  const selectedSet = useMemo(() => new Set(value), [value])

  // Include selected options missing from the current page so labels/checks stay
  // correct across searches.
  const mergedOptions = useMemo(() => {
    const byValue = new Map(options.map((o) => [o.value, o]))
    for (const o of selectedOptions) {
      if (!byValue.has(o.value)) byValue.set(o.value, o)
    }
    return [...byValue.values()]
  }, [options, selectedOptions])

  const labelByValue = useMemo(() => {
    const map = new Map(mergedOptions.map((o) => [o.value, o.label]))
    return map
  }, [mergedOptions])

  const handleValueToggle = (val) => {
    const next = selectedSet.has(val)
      ? value.filter((v) => v !== val)
      : [...value, val]
    onChange?.(next)
  }

  const handleValueRemove = (val) => onChange?.(value.filter((v) => v !== val))

  const pills = value.map((val) => {
    const label = labelByValue.get(val)
    return (
      <Pill
        key={val}
        withRemoveButton
        disabled={disabled}
        onRemove={() => handleValueRemove(val)}
        className={classes.pill}
      >
        {label != null ? (
          label
        ) : (
          <Skeleton
            className={classes.skeleton}
            height={10}
            width={110}
            radius="xl"
          />
        )}
      </Pill>
    )
  })

  // Skip pending (null-label) selections in the dropdown so it has no empty rows.
  const optionNodes = mergedOptions.filter((o) => o.label != null).map((o) => {
    const checked = selectedSet.has(o.value)
    return (
      <Combobox.Option value={o.value} key={o.value} active={checked}>
        <Group justify="space-between" gap="sm" wrap="nowrap">
          <span>{o.label}</span>
          {checked && <Check size={16} />}
        </Group>
      </Combobox.Option>
    )
  })

  const isEmpty = optionNodes.length === 0 && !loading

  return (
    <Combobox store={combobox} onOptionSubmit={handleValueToggle} disabled={disabled}>
      <Combobox.DropdownTarget>
        <PillsInput
          label={label}
          required={required}
          disabled={disabled}
          onClick={() => combobox.openDropdown()}
        >
          <Pill.Group>
            {pills}
            <Combobox.EventsTarget>
              <PillsInput.Field
                value={searchValue}
                placeholder={value.length === 0 ? placeholder : ''}
                disabled={disabled}
                onFocus={() => combobox.openDropdown()}
                onChange={(event) => {
                  combobox.openDropdown()
                  onSearchChange?.(event.currentTarget.value)
                }}
                onKeyDown={(event) => {
                  if (event.key === 'Backspace' && searchValue.length === 0) {
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
          <Box ref={setViewport} className={classes.viewport} mah={240}>
            {isEmpty ? (
              <Combobox.Empty>Nothing found</Combobox.Empty>
            ) : (
              optionNodes
            )}
            {loading && (
              <Group justify="center" py="xs">
                <Loader size="xs" />
                <Text size="xs" c="dimmed">
                  Loading…
                </Text>
              </Group>
            )}
            <div ref={sentinelRef} className={classes.sentinel} />
          </Box>
        </Combobox.Options>
      </Combobox.Dropdown>
    </Combobox>
  )
}
