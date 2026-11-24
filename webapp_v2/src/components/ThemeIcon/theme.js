import { ThemeIcon } from '@mantine/core'
import classes from './ThemeIcon.module.css'

// Every ThemeIcon (the icon tile on cards, promotions and steps) takes its
// corners from --hoop-icon-radius (theme.js).
export const ThemeIconTheme = ThemeIcon.extend({
  classNames: { root: classes.root },
})
