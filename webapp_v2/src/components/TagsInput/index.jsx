import { TagsInput as MantineTagsInput } from '@mantine/core'

/**
 * Multi-tag creatable input. Each tag becomes a chip; press Enter to commit.
 *
 * Usage:
 *   <TagsInput
 *     label="Command Arguments"
 *     value={args}
 *     onChange={setArgs}
 *     splitChars={[',']}
 *   />
 */
export default function TagsInput(props) {
  return <MantineTagsInput radius="sm" {...props} />
}
