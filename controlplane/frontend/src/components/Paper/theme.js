import { Paper } from '@mantine/core'
import classes from './Paper.module.css'

// Global border color for Paper (and Card, which renders through Paper).
// Only visible on instances with the `withBorder` prop; keeps card/panel
// borders in step with the app-wide input border.
export const PaperTheme = Paper.extend({
  classNames: { root: classes.root },
})
