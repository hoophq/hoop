import { MultiSelect as MantineMultiSelect } from '@mantine/core'
import classes from './MultiSelect.module.css'

/**
 * Multi-value select input.
 *
 * Usage:
 *   <MultiSelect
 *     label="Groups"
 *     data={groups}
 *     value={selectedGroups}
 *     onChange={setSelectedGroups}
 *     placeholder="Select groups..."
 *   />
 */
export default function MultiSelect({
  classNames: callerClassNames,
  placeholder,
  value,
  ...props
}) {
  return (
    <MantineMultiSelect
      classNames={{ pill: classes.pill, ...callerClassNames }}
      // Once a pill is in the field the placeholder sits beside it and reads
      // as though nothing were selected. Drop it, the way
      // PaginatedMultiSelect already does. Uncontrolled inputs have no value
      // to inspect, so they keep theirs.
      placeholder={value?.length ? undefined : placeholder}
      value={value}
      {...props}
    />
  )
}
