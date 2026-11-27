import { useRef, useState } from 'react'
import { Group, Stack } from '@mantine/core'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import TagsInput from '@/components/TagsInput'
import { useSidecarStore } from '@/stores/useSidecarStore'
import { showSnackbar } from '@/utils/snackbar'
import { normalizeOwners, ownersFieldError } from '../owners'

// Mounted on open, so the field starts from the owners on screen each time.
function Form({ sidecar, owners, onClose }) {
  const saveOwners = useSidecarStore((s) => s.saveOwners)
  const [value, setValue] = useState(() => owners.map((owner) => owner.email))
  const [error, setError] = useState(null)
  const [saving, setSaving] = useState(false)
  const input = useRef(null)

  const refuse = (message) => {
    setError(message)
    input.current?.focus()
  }

  const save = async () => {
    const next = normalizeOwners(value)
    const invalid = ownersFieldError(next)
    if (invalid) {
      refuse(invalid)
      return
    }
    setSaving(true)
    const { ok, error: refusal } = await saveOwners(sidecar.id, next)
    setSaving(false)
    if (!ok) {
      const message = refusal?.response?.data?.message ?? refusal?.message
      if (refusal?.response?.status === 422) refuse(message || 'Owners must be a list of at most 20 emails.')
      showSnackbar({ level: 'error', text: 'Failed to save owners.', description: message })
      return
    }
    showSnackbar({ level: 'success', text: `Owners of "${sidecar.name}" saved.` })
    onClose()
  }

  return (
    <Stack gap="lg">
      <TagsInput
        ref={input}
        label="Owners"
        description="The people responsible for this Sidecar. Use the email of each Hoop user."
        placeholder="ana@example.com"
        splitChars={[',', ';', ' ']}
        value={value}
        onChange={(next) => {
          setValue(next)
          setError(null)
        }}
        error={error}
        disabled={saving}
        data-autofocus
      />
      <Group justify="flex-end" gap="sm">
        <Button variant="subtle" color="gray" onClick={onClose} disabled={saving}>
          Cancel
        </Button>
        <Button onClick={save} loading={saving}>
          Save
        </Button>
      </Group>
    </Stack>
  )
}

export default function OwnersModal({ opened, sidecar, owners, onClose }) {
  return (
    <Modal opened={opened} onClose={onClose} title="Edit owners" closeOnClickOutside={false}>
      <Form sidecar={sidecar} owners={owners} onClose={onClose} />
    </Modal>
  )
}
