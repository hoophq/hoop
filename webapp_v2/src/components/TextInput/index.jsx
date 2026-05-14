import { TextInput as MantineTextInput } from '@mantine/core'

/**
 * Standard text input field.
 *
 * Usage:
 *   <TextInput label="Name" placeholder="e.g. my-api-key" value={name} onChange={(e) => setName(e.currentTarget.value)} />
 */
export default function TextInput(props) {
  return <MantineTextInput radius="sm" {...props} />
}
