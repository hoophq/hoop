import { useEffect, useState } from 'react'
import { Box, Flex, Group, Loader, Popover, Stack, Text } from '@mantine/core'
import { useIntersection } from '@mantine/hooks'
import { Check, Search, X } from 'lucide-react'
import Button from '@/components/Button'
import TextInput from '@/components/TextInput'
import classes from './AsyncValueFilter.module.css'

/**
 * Single-value filter dropdown over a paginated, server-searched option source —
 * the async counterpart of `ValueFilter`. `onSelect` receives the option label.
 *
 * Usage:
 *   <AsyncValueFilter
 *     icon={Shapes} label="Resource Role"
 *     selected={selected} onSelect={setSelected} onClear={() => setSelected(null)}
 *     options={options} loading={loading} hasMore={hasMore} onLoadMore={loadMore}
 *     searchValue={search} onSearchChange={setSearch} onOpen={ensureLoaded}
 *   />
 */
export default function AsyncValueFilter({
  icon,
  label,
  placeholder,
  selected,
  onSelect,
  onClear,
  options = [],
  loading = false,
  hasMore = false,
  onLoadMore,
  searchValue = '',
  onSearchChange,
  onOpen,
}) {
  const Icon = icon
  const [open, setOpen] = useState(false)
  const [viewport, setViewport] = useState(null)
  const { ref: sentinelRef, entry } = useIntersection({ root: viewport, threshold: 1 })

  useEffect(() => {
    if (entry?.isIntersecting && hasMore && !loading) onLoadMore?.()
  }, [entry?.isIntersecting, hasMore, loading, onLoadMore])

  const hasSelected = typeof selected === 'string' && selected.trim() !== ''

  const close = () => {
    setOpen(false)
    onSearchChange?.('')
  }

  const handleTrigger = () => {
    const next = !open
    setOpen(next)
    if (next) onOpen?.()
  }

  return (
    <Popover
      opened={open}
      onChange={setOpen}
      position="bottom-start"
      width={320}
      withinPortal
    >
      <Popover.Target>
        <Button
          variant={hasSelected ? 'light' : 'default'}
          color="gray"
          onClick={handleTrigger}
          leftSection={<Icon size={16} />}
          rightSection={
            hasSelected ? (
              <X
                size={14}
                onClick={(event) => {
                  event.stopPropagation()
                  onClear()
                  close()
                }}
              />
            ) : null
          }
        >
          {hasSelected ? selected : label}
        </Button>
      </Popover.Target>
      <Popover.Dropdown p="xs">
        <Stack gap="xs">
          {hasSelected && (
            <Box
              px="sm"
              py="xs"
              className={classes.row}
              onClick={() => {
                onClear()
                close()
              }}
            >
              <Text size="sm" c="dimmed">
                Clear filter
              </Text>
            </Box>
          )}
          <TextInput
            placeholder={placeholder}
            value={searchValue}
            onChange={(event) => onSearchChange?.(event.currentTarget.value)}
            leftSection={<Search size={14} />}
            size="xs"
          />
          <Box ref={setViewport} className={classes.scroll} mah={288}>
            {options.length > 0 ? (
              <Stack gap={0}>
                {options.map((option) => (
                  <Flex
                    key={option.value}
                    align="center"
                    justify="space-between"
                    px="sm"
                    py="xs"
                    className={classes.row}
                    onClick={() => {
                      onSelect(option.label)
                      close()
                    }}
                  >
                    <Text size="sm" lineClamp={1}>
                      {option.label}
                    </Text>
                    {option.label === selected && <Check size={14} />}
                  </Flex>
                ))}
              </Stack>
            ) : (
              !loading && (
                <Box px="sm" py="md">
                  <Text size="xs" c="dimmed" fs="italic">
                    {searchValue
                      ? `No ${label.toLowerCase()} found`
                      : `No ${label.toLowerCase()} available`}
                  </Text>
                </Box>
              )
            )}
            {loading && (
              <Group justify="center" py="xs">
                <Loader size="xs" />
              </Group>
            )}
            <div ref={sentinelRef} className={classes.sentinel} />
          </Box>
        </Stack>
      </Popover.Dropdown>
    </Popover>
  )
}
