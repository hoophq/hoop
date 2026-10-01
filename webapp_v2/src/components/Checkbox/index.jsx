import { Checkbox as MantineCheckbox } from '@mantine/core'

export default function Checkbox({ radius = 'xs', ...props }) {
  return <MantineCheckbox radius={radius} {...props} />
}
