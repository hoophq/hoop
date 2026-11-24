import { Alert } from '@mantine/core'
import classes from './Alert.module.css'

// Every Alert takes its corners from --hoop-callout-radius (theme.js).
export const AlertTheme = Alert.extend({
  classNames: { root: classes.root },
})
