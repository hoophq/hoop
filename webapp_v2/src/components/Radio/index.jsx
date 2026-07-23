import { Radio as MantineRadio } from '@mantine/core'

function Radio(props) {
  return <MantineRadio {...props} />
}

Radio.Group = MantineRadio.Group
// Input-less radio visual — safe to render inside buttons/cards where a real
// <input> would produce invalid nested-interactive markup.
Radio.Indicator = MantineRadio.Indicator

export default Radio
