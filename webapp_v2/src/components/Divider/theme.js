import { Divider } from '@mantine/core'
import classes from './Divider.module.css'

// Every Divider takes the theme's subtle border color.
export const DividerTheme = Divider.extend({
  classNames: { root: classes.root },
})
