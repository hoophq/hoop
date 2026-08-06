import { Group, Stack, Text } from '@mantine/core'
import { CircleX, Search } from 'lucide-react'
import Button from '@/components/Button'
import TextInput from '@/components/TextInput'

export function SearchBar({ query, onQueryChange, onClearQuery, total, matched, shown, truncated }) {
  const searching = query.trim().length > 0

  let countLabel
  if (searching && truncated) {
    // Both the render cap and the query are biting — report against the
    // matches, otherwise the number reads as a share of the whole list.
    countLabel = `Showing ${shown} of ${matched} results — refine your search`
  } else if (searching) {
    countLabel = `Showing ${matched} of ${total} results.`
  } else if (truncated) {
    countLabel = `Showing ${shown} of ${total} Resource Roles — refine your search`
  } else {
    countLabel = `Showing ${total} Resource Role${total === 1 ? '' : 's'}`
  }

  return (
    <Stack gap="xs" px="md" pb="sm">
      <TextInput
        data-autofocus
        value={query}
        onChange={(e) => onQueryChange(e.currentTarget.value)}
        placeholder="Look for type, attributes, tag or names"
        leftSection={<Search size={16} aria-hidden="true" />}
        aria-label="Search native connections"
      />
      <Group gap="sm">
        <Text fz="xs" c="dimmed" role="status" aria-live="polite">
          {countLabel}
        </Text>
        {searching && (
          // On the app's control scale like every other button here: size="xs"
          // is 24px in components/Button/theme.js. `compact-xs` with a pill
          // radius was a one-off that sat outside that scale. The `light`
          // variant keeps the soft indigo it always had.
          <Button
            variant="light"
            size="xs"
            leftSection={<CircleX size={12} aria-hidden="true" />}
            onClick={onClearQuery}
          >
            Dismiss search
          </Button>
        )}
      </Group>
    </Stack>
  )
}
