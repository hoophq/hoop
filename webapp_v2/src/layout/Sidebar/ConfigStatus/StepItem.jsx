import { Fragment } from 'react'
import { Box, Collapse, Text, UnstyledButton } from '@mantine/core'
import { Check, ChevronDown, ChevronUp } from 'lucide-react'
import { SubItem } from './SubItem'
import classes from './ConfigStatus.module.css'

export function StepItem({ step, opened, onToggle, onNavigate }) {
  const Chevron = opened ? ChevronUp : ChevronDown

  return (
    <Box className={classes.step}>
      <UnstyledButton className={classes.stepHeader} aria-expanded={opened} onClick={onToggle}>
        {step.done ? (
          <Box className={classes.stepDoneIcon} aria-hidden="true">
            <Check size={14} strokeWidth={3} />
          </Box>
        ) : (
          <step.icon size={24} aria-hidden="true" className={classes.stepIcon} />
        )}
        <Text component="span" className={classes.stepTitle} data-done={step.done || undefined}>
          {step.title}
        </Text>
        <Chevron size={16} aria-hidden="true" className={classes.chevron} />
      </UnstyledButton>

      <Collapse in={opened}>
        <Box className={classes.subItems}>
          {step.subItems.map((item) => (
            <Fragment key={item.checkKey}>
              {item.dividerBefore && <Box className={classes.divider} role="separator" />}
              <SubItem item={item} onNavigate={onNavigate} />
            </Fragment>
          ))}
        </Box>
      </Collapse>
    </Box>
  )
}
