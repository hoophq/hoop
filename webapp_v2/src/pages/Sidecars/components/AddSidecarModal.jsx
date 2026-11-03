import { Stack } from '@mantine/core'
import Modal from '@/components/Modal'
import SidecarMethodCards from './SidecarMethodCards'

// "Add new Sidecar" from a filled list (Figma: "Connect a Sidecar" modal). The
// button that opens it is disabled once a free organization holds its sidecar.
export default function AddSidecarModal({ opened, onClose }) {
  return (
    <Modal opened={opened} onClose={onClose} title="Connect a Sidecar" size="lg">
      <Stack gap="lg">
        <SidecarMethodCards onPick={onClose} />
      </Stack>
    </Modal>
  )
}
