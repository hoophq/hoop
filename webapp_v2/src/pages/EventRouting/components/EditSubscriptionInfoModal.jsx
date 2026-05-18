import { useEffect, useState } from 'react'
import { Button, Group, Stack } from '@mantine/core'
import { notifications } from '@mantine/notifications'
import Modal from '@/components/Modal'
import TextInput from '@/components/TextInput'
import Textarea from '@/components/Textarea'
import { useEventRoutingStore } from '../store'

export default function EditSubscriptionInfoModal({ sub, opened, onClose }) {
  const updateSubscription = useEventRoutingStore((s) => s.updateSubscription)
  const submitting = useEventRoutingStore((s) => s.submitting)
  const [name, setName] = useState(sub?.name || '')
  const [description, setDescription] = useState(sub?.description || '')

  useEffect(() => {
    if (opened) {
      setName(sub?.name || '')
      setDescription(sub?.description || '')
    }
  }, [opened, sub])

  const canSave = name.trim().length > 0
  const handleSave = async () => {
    if (!canSave || !sub) return
    try {
      await updateSubscription(sub.id, {
        ...sub,
        name: name.trim(),
        description: description.trim(),
      })
      notifications.show({ message: 'Subscription updated.', color: 'green' })
      onClose()
    } catch (e) {
      notifications.show({
        message: e?.response?.data?.message || 'Failed to update subscription.',
        color: 'red',
      })
    }
  }

  return (
    <Modal opened={opened} onClose={onClose} title="Edit subscription" size="md">
      <Stack gap="md">
        <TextInput
          label="Name"
          value={name}
          onChange={(e) => {
            const v = e.currentTarget.value
            setName(v)
          }}
          required
          autoFocus
        />
        <Textarea
          label="Description (Optional)"
          placeholder="What this subscription is for and when it should fire"
          value={description}
          onChange={(e) => {
            const v = e.currentTarget.value
            setDescription(v)
          }}
        />
        <Group justify="flex-end" mt="xs">
          <Button variant="subtle" color="gray" onClick={onClose}>Cancel</Button>
          <Button onClick={handleSave} disabled={!canSave} loading={submitting}>
            Save
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
