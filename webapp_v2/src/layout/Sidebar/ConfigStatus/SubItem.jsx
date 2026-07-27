import { UnstyledButton, Text } from '@mantine/core'
import { CircleDashed, CircleCheckBig, ArrowRight } from 'lucide-react'
import classes from './ConfigStatus.module.css'

export function SubItem({ item, onNavigate }) {
  const Icon = item.done ? CircleCheckBig : item.icon ?? CircleDashed

  return (
    <UnstyledButton
      className={classes.subItem}
      data-done={item.done || undefined}
      disabled={item.done}
      onClick={() => onNavigate(item)}
    >
      <Icon size={16} aria-hidden="true" className={classes.subItemIcon} />
      <Text component="span" className={classes.subItemLabel}>
        {item.label}
      </Text>
      {!item.done && <ArrowRight size={12} aria-hidden="true" className={classes.subItemArrow} />}
    </UnstyledButton>
  )
}
