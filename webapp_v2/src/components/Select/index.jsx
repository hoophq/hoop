import { Select as MantineSelect } from '@mantine/core'

/**
 * Single-value select input.
 *
 * Usage:
 *   <Select
 *     label="Status"
 *     data={[{ value: 'active', label: 'Active' }, { value: 'inactive', label: 'Inactive' }]}
 *     value={value}
 *     onChange={setValue}
 *   />
 */
export default function Select(props) {
  return <MantineSelect radius="sm" {...props} />
}
