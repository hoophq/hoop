import { Code } from '@mantine/core'
import classes from './Code.module.css'

// `variant="accent"` marks an identifier (an event name), `accent-outline` the
// same with a border; `variant="terminal"` is a dark payload block.
export const CodeTheme = Code.extend({
  classNames: { root: classes.root },
})
