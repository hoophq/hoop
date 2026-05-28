import { Menu, ActionIcon } from '@mantine/core'
import { MoreHorizontal } from 'lucide-react'
import classes from './ActionMenu.module.css'

/**
 * Dropdown action menu for table rows and cards.
 *
 * Usage:
 *   <ActionMenu>
 *     <ActionMenu.Item onClick={handleEdit}>Edit</ActionMenu.Item>
 *     <ActionMenu.Item danger onClick={handleDelete}>Delete</ActionMenu.Item>
 *   </ActionMenu>
 */
function ActionMenu({ children, disabled = false }) {
  return (
    <Menu shadow="md" width={180} position="bottom-end" withinPortal classNames={{ dropdown: classes.dropdown }}>
      <Menu.Target>
        <ActionIcon variant="subtle" color="gray" disabled={disabled} aria-label="Actions">
          <MoreHorizontal size={16} />
        </ActionIcon>
      </Menu.Target>
      <Menu.Dropdown>{children}</Menu.Dropdown>
    </Menu>
  )
}

function ActionMenuItem({ danger = false, onClick, disabled = false, children }) {
  return (
    <Menu.Item
      onClick={onClick}
      disabled={disabled}
      classNames={{ item: danger ? classes.itemDanger : classes.item }}
    >
      {children}
    </Menu.Item>
  )
}

ActionMenu.Item = ActionMenuItem
ActionMenu.Divider = Menu.Divider

export default ActionMenu
