import { Group, Stack, Text, VisuallyHidden } from '@mantine/core'
import { useDebouncedValue } from '@mantine/hooks'
import { useRef } from 'react'
import { CircleX, Search } from 'lucide-react'
import Button from '@/components/Button'
import TextInput from '@/components/TextInput'

function countLabelFor({ searching, truncated, shown, matched, total }) {
  if (searching && truncated) {
    // Both the render cap and the query are biting — report against the
    // matches, otherwise the number reads as a share of the whole list.
    return `Showing ${shown} of ${matched} results — refine your search`
  }
  if (searching) return `Showing ${matched} of ${total} results.`
  if (truncated) return `Showing ${shown} of ${total} Resource Roles — refine your search`
  return `Showing ${total} Resource Role${total === 1 ? '' : 's'}`
}

export function SearchBar({ query, onQueryChange, onClearQuery, total, matched, shown, truncated }) {
  const searching = query.trim().length > 0
  const inputRef = useRef(null)

  const countLabel = countLabelFor({ searching, truncated, shown, matched, total })

  // The visible text updates as you type; the announcement lags it. A polite
  // live region rewritten on every keystroke turns typing into a stream of
  // interruptions for a screen reader.
  const [announced] = useDebouncedValue(countLabel, 500)

  const clear = () => {
    onClearQuery()
    // This button destroys itself on click, so send focus somewhere deliberate
    // instead of letting it fall to <body>.
    inputRef.current?.focus()
  }

  return (
    <Stack gap="xs" px="md" pb="sm">
      <TextInput
        ref={inputRef}
        data-autofocus
        value={query}
        onChange={(e) => onQueryChange(e.currentTarget.value)}
        placeholder="Look for type, attributes, tag or names"
        leftSection={<Search size={16} aria-hidden="true" />}
        aria-label="Search native connections"
      />
      <Group gap="sm">
        <Text fz="xs" c="dimmed" aria-hidden="true">
          {countLabel}
        </Text>
        <VisuallyHidden role="status" aria-live="polite">
          {announced}
        </VisuallyHidden>
        {searching && (
          // On the app's control scale like every other button here: size="xs"
          // is 24px in components/Button/theme.js. `compact-xs` with a pill
          // radius was a one-off that sat outside that scale. The `light`
          // variant keeps the soft indigo it always had.
          <Button
            variant="light"
            size="xs"
            leftSection={<CircleX size={12} aria-hidden="true" />}
            onClick={clear}
          >
            Dismiss search
          </Button>
        )}
      </Group>
    </Stack>
  )
}
