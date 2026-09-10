import { Stepper } from '@mantine/core'
import classes from './Stepper.module.css'

// Global look for the Stepper: a soft rule between steps, the Figma's light
// fill and blue ring on a step not reached yet, and the accent on every step
// from the current one back. The rationale for each rule, and why none of it
// can go through cssVariablesResolver, is in Stepper.module.css.
export const StepperTheme = Stepper.extend({
  classNames: { root: classes.root, stepIcon: classes.stepIcon },
})
