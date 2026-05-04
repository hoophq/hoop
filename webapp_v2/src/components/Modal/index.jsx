import { Modal as MantineModal } from '@mantine/core'
import classes from './Modal.module.css'

/**
 * Application modal dialog.
 *
 * Usage:
 *   <Modal opened={opened} onClose={close} title="Add User" size="md">
 *     {children}
 *   </Modal>
 */
export default function Modal({ children, size = 'md', ...props }) {
  return (
    <MantineModal size={size} radius="md" centered classNames={{ title: classes.title }} {...props}>
      {children}
    </MantineModal>
  )
}
