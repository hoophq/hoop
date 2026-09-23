import { Stack } from '@mantine/core'
import Modal from '@/components/Modal'
import SidecarMethodCards from './SidecarMethodCards'

// "Add new Sidecar" from a filled list (Figma: "Connect a Sidecar" modal). Only
// reachable with an Enterprise license: the button that opens it is disabled
// on the free plan.
export default function AddSidecarModal({ opened, onClose }) {
  return (
    <Modal opened={opened} onClose={onClose} title="Connect a Sidecar" size="lg">
      <Stack gap="lg">
        <SidecarMethodCards onPick={onClose} />
      </Stack>
    </Modal>
  )
}
