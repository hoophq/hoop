import { Radio as MantineRadio } from '@mantine/core'

function Radio(props) {
  return <MantineRadio {...props} />
}

Radio.Group = MantineRadio.Group

export default Radio
