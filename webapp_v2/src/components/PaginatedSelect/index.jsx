import { useRef } from 'react'
import {
  Combobox,
  Group,
  Input,
  InputBase,
  Loader,
  ScrollArea,
  Text,
  useCombobox,
} from '@mantine/core'

/**
 * Single-value select over a paginated, server-searched option source
 * (infinite scroll), with the search box in the dropdown. Controlled — the
 * caller owns fetching. `selectedLabel` names a value that is not on the
 * loaded pages.
 *
 * Usage:
 *   <PaginatedSelect
 *     label="Server" value={id} selectedLabel={name} onChange={(option) => ...}
 *     options={options} loading={loading} hasMore={hasMore} onLoadMore={loadMore}
 *     searchValue={search} onSearchChange={setSearch}
 *   />
 */
export default function PaginatedSelect({
  label,
  placeholder = 'Select an option',
  searchPlaceholder = 'Search options...',
  nothingFound = 'No options found',
  required = false,
  error,
  disabled = false,
  value = null,
  selectedLabel = null,
  onChange,
  options = [],
  loading = false,
  hasMore = false,
  onLoadMore,
  searchValue = '',
  onSearchChange,
  onDropdownOpen,
  comboboxProps,
}) {
  const combobox = useCombobox({
    onDropdownOpen: () => {
      combobox.focusSearchInput()
      onDropdownOpen?.()
    },
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

  const shown = selectedLabel ?? options.find((o) => o.value === value)?.label

  const handleOptionSubmit = (val) => {
    const option = options.find((o) => o.value === val)
    if (option) onChange?.(option)
    combobox.closeDropdown()
  }

  return (
    <Combobox store={combobox} onOptionSubmit={handleOptionSubmit} {...comboboxProps}>
      <Combobox.Target>
        <InputBase
          component="button"
          type="button"
          pointer
          label={label}
          withAsterisk={required}
          error={error}
          disabled={disabled}
          rightSection={<Combobox.Chevron />}
          rightSectionPointerEvents="none"
          onClick={() => combobox.toggleDropdown()}
        >
          {value != null && shown != null ? shown : <Input.Placeholder>{placeholder}</Input.Placeholder>}
        </InputBase>
      </Combobox.Target>

      <Combobox.Dropdown>
        <Combobox.Search
          value={searchValue}
          placeholder={searchPlaceholder}
          onChange={(event) => onSearchChange?.(event.currentTarget.value)}
        />
        <Combobox.Options>
          <ScrollArea.Autosize
            mah={240}
            type="auto"
            viewportRef={viewportRef}
            onScrollPositionChange={handleScrollPositionChange}
          >
            {options.length === 0 && !loading ? (
              <Combobox.Empty>{nothingFound}</Combobox.Empty>
            ) : (
              options.map((o) => (
                <Combobox.Option value={o.value} key={o.value} active={o.value === value}>
                  {o.label}
                </Combobox.Option>
              ))
            )}
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
