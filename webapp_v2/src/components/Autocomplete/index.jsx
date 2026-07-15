import { Autocomplete as MantineAutocomplete } from '@mantine/core'

/**
 * Single-value combobox: free-typing input with autocompleted suggestions.
 *
 * Usage:
 *   <Autocomplete
 *     label="Key"
 *     data={['team', 'environment', 'region']}
 *     value={value}
 *     onChange={setValue}
 *   />
 */
export default function Autocomplete(props) {
  return <MantineAutocomplete radius="sm" {...props} />
}
