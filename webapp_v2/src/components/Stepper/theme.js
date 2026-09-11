import { Stepper } from '@mantine/core'
import classes from './Stepper.module.css'

// Softens the gray of a step not reached yet and of the rule between steps.
// Why it cannot go through cssVariablesResolver, and why it stops at one
// variable, is in Stepper.module.css.
export const StepperTheme = Stepper.extend({
  classNames: { root: classes.root },
})
