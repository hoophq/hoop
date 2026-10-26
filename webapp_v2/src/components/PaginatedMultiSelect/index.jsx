import { useMemo, useRef } from 'react'
import {
  Combobox,
  Group,
  Loader,
  Pill,
  PillsInput,
  ScrollArea,
  Skeleton,
  Text,
  useCombobox,
} from '@mantine/core'
import RuleTrafficBadge from '@/components/RuleTrafficBadge'
import { TRAFFIC_SIDECAR } from '@/utils/ruleTraffic'
import classes from './PaginatedMultiSelect.module.css'

// An option `{ value, label, sidecar }`: a sidecar one carries the Sidecar tag.
// A pill is sized at min-content, so the tag keeps its full label.
function OptionLabel({ option }) {
  if (!option.sidecar) return option.label
  return (
    <Group gap="xs" wrap="nowrap" component="span">
      {option.label}
      <RuleTrafficBadge traffic={TRAFFIC_SIDECAR} fullLabel />
    </Group>
  )
}

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

  const viewportRef = useRef(null)

  const handleScrollPositionChange = () => {
    if (!hasMore || loading) return
    const el = viewportRef.current
    if (!el) return
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 50) {
      onLoadMore?.()
    }
  }

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

  const optionByValue = useMemo(
    () => new Map(mergedOptions.map((o) => [o.value, o])),
    [mergedOptions],
  )

  const handleValueToggle = (val) => {
    const next = selectedSet.has(val)
      ? value.filter((v) => v !== val)
      : [...value, val]
    onChange?.(next)
    // Clear the search on select — matches Mantine's default MultiSelect.
    onSearchChange?.('')
  }

  const handleValueRemove = (val) => onChange?.(value.filter((v) => v !== val))

  const pills = value.map((val) => {
    const option = optionByValue.get(val)
    return (
      <Pill
        key={val}
        withRemoveButton
        disabled={disabled}
        onRemove={() => handleValueRemove(val)}
      >
        {option?.label != null ? (
          <OptionLabel option={option} />
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

  const optionNodes = mergedOptions
    .filter((o) => o.label != null && !selectedSet.has(o.value))
    .map((o) => (
      <Combobox.Option value={o.value} key={o.value}>
        <OptionLabel option={o} />
      </Combobox.Option>
    ))

  const isEmpty = optionNodes.length === 0 && !loading && !hasMore

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
          <ScrollArea.Autosize
            mah={240}
            type="auto"
            viewportRef={viewportRef}
            onScrollPositionChange={handleScrollPositionChange}
          >
            {isEmpty ? <Combobox.Empty>Nothing found</Combobox.Empty> : optionNodes}
            {loading && (
              <Group justify="center" py="xs">
                <Loader size="xs" />
                <Text size="xs" c="dimmed">
                  Loading…
                </Text>
              </Group>
            )}
          </ScrollArea.Autosize>
        </Combobox.Options>
      </Combobox.Dropdown>
    </Combobox>
  )
}
