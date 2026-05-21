import { Textarea as MantineTextarea } from '@mantine/core'

/**
 * Textarea wrapper. Defaults to autosize between 2 and 6 rows.
 */
export default function Textarea(props) {
  return <MantineTextarea autosize minRows={2} maxRows={6} {...props} />
}
