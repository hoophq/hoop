import { Stepper as MantineStepper } from '@mantine/core'

/**
 * Horizontal step indicator for multi-page flows (the Figma "Stepper": numbered
 * circles joined by a rule). Re-exports Stepper.Step so call sites never import
 * from Mantine directly. Controlled: `active` is the zero-based current step.
 *
 * Usage:
 *   <Stepper active={step}>
 *     <Stepper.Step label="Connect" />
 *     <Stepper.Step label="Configure" />
 *     <Stepper.Step label="Overview" />
 *   </Stepper>
 *
 * Renders the indicator only: pass no children to a Step and place the body
 * below the Stepper yourself, so each step can own its layout.
 */
function Stepper(props) {
  return <MantineStepper size="sm" iconSize={32} allowNextStepsSelect={false} {...props} />
}

Stepper.Step = MantineStepper.Step
Stepper.Completed = MantineStepper.Completed

export default Stepper
