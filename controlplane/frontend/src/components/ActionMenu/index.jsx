import { Menu, ActionIcon } from '@mantine/core'
import { MoreHorizontal } from 'lucide-react'
import classes from './ActionMenu.module.css'

/**
 * Dropdown action menu for table rows, cards and header controls.
 *
 * Usage:
 *   <ActionMenu>
 *     <ActionMenu.Item onClick={handleEdit}>Edit</ActionMenu.Item>
 *     <ActionMenu.Item danger onClick={handleDelete}>Delete</ActionMenu.Item>
 *   </ActionMenu>
 *
 * Pass `target` to replace the default kebab trigger with your own element
 * (it must forward a ref and accept the props Menu.Target injects):
 *   <ActionMenu target={<UnstyledButton>…</UnstyledButton>} width={240}>
 */
function ActionMenu({
  children,
  disabled = false,
  target = null,
  width = 180,
  position = 'bottom-end',
}) {
  return (
    <Menu shadow="md" width={width} position={position} withinPortal>
      <Menu.Target>
        {target ?? (
          <ActionIcon variant="subtle" color="gray" disabled={disabled} aria-label="Actions">
            <MoreHorizontal size={16} />
          </ActionIcon>
        )}
      </Menu.Target>
      <Menu.Dropdown>{children}</Menu.Dropdown>
    </Menu>
  )
}

// `...rest` is forwarded so call sites can pass leftSection, id (the Intercom
// launcher hooks onto one), and the other Menu.Item props.
function ActionMenuItem({ danger = false, onClick, disabled = false, children, ...rest }) {
  return (
    <Menu.Item
      {...rest}
      onClick={onClick}
      disabled={disabled}
      classNames={danger ? { item: classes.itemDanger } : undefined}
    >
      {children}
    </Menu.Item>
  )
}

ActionMenu.Item = ActionMenuItem
ActionMenu.Divider = Menu.Divider
ActionMenu.Label = Menu.Label

export default ActionMenu
