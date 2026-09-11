import { Group, Stack } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import ListenerForm from '../components/ListenerForm'
import { useListenerEditor } from '../useListenerEditor'

// Shell A: the listener form in a modal, opened from the details page.
//
// Render it only while open. The editor seeds its state from the listener it
// was given, so a mounted-but-hidden modal would keep the previous row's
// values.
export default function ListenerModal({ sidecar, index, onClose, onSaved }) {
  const { form, setField, errors, saving, save, isNew } = useListenerEditor({ sidecar, index })

  const handleSave = async () => {
    const updated = await save()
    if (updated) {
      onSaved(updated)
      onClose()
    }
  }

  return (
    <Modal opened onClose={onClose} title={isNew ? 'Add listener' : 'Edit listener'} size="lg">
      <Stack gap="lg">
        <ListenerForm form={form} setField={setField} errors={errors} />
        <Group justify="flex-end">
          <Button variant="subtle" color="gray" onClick={onClose} disabled={saving}>
            Cancel
          </Button>
          <Button onClick={handleSave} loading={saving}>
            {isNew ? 'Add listener' : 'Save listener'}
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
