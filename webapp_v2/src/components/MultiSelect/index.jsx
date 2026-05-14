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
export default function MultiSelect({ classNames: callerClassNames, ...props }) {
  return (
    <MantineMultiSelect
      radius="sm"
      classNames={{ pill: classes.pill, ...callerClassNames }}
      {...props}
    />
  )
}
