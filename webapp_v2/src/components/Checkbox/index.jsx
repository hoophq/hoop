import { Checkbox as MantineCheckbox } from '@mantine/core'

// Corners come from the theme (components/Checkbox/theme.js).
export default function Checkbox(props) {
  return <MantineCheckbox {...props} />
}

Checkbox.Group = MantineCheckbox.Group
Checkbox.Card = MantineCheckbox.Card
Checkbox.Indicator = MantineCheckbox.Indicator
