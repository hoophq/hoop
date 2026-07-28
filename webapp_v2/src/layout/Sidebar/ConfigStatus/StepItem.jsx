import { Fragment } from 'react';
import { Box, Collapse, Text, UnstyledButton } from '@mantine/core';
import { CircleCheckBig, ChevronDown, ChevronUp } from 'lucide-react';
import ActionIcon from '@/components/ActionIcon';
import { SubItem } from './SubItem';
import classes from './ConfigStatus.module.css';

export function StepItem({ step, opened, onToggle, onNavigate }) {
  const Chevron = opened ? ChevronUp : ChevronDown;

  return (
    <Box className={classes.step}>
      <UnstyledButton className={classes.stepHeader} aria-expanded={opened} onClick={onToggle}>
        {/* Action-button look only — component="div" keeps it non-interactive
            (no nested button); the click lives on the whole row. */}
        {step.done
          ? <ActionIcon
              component="div"
              variant="light"
              color="green"
              size="sm"
              radius="xl"
              aria-hidden="true"
              className={classes.stepActionIcon}
            >
              <CircleCheckBig size={16} />
            </ActionIcon>
          : <ActionIcon
              component="div"
              variant="light"
              color="gray"
              size="sm"
              radius="xl"
              aria-hidden="true"
              className={classes.stepActionIcon}
            >
              <step.icon size={16} className={classes.stepIcon} strokeWidth={1} />
            </ActionIcon>}
        <Text component="span" className={classes.stepTitle} data-done={step.done || undefined}>
          {step.title}
        </Text>
        <Chevron size={16} aria-hidden="true" className={classes.chevron} />
      </UnstyledButton>

      <Collapse in={opened}>
        <Box className={classes.subItems}>
          {step.subItems.map(item =>
            <Fragment key={item.checkKey}>
              {item.dividerBefore && <Box className={classes.divider} role="separator" />}
              <SubItem item={item} onNavigate={onNavigate} />
            </Fragment>
          )}
        </Box>
      </Collapse>
    </Box>
  );
}
