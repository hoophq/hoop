import { Spotlight } from '@mantine/spotlight'
import classes from './Spotlight.module.css'

export const SpotlightTheme = Spotlight.extend({
  classNames: {
    actionsGroup: classes.actionsGroup,
    actionLabel: classes.actionLabel,
    actionsList: classes.actionsList,
  },
})
