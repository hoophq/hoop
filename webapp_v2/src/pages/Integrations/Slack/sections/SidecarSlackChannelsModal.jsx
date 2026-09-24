import { useEffect, useState } from 'react'
import { Group, Stack, Text } from '@mantine/core'
import { Info } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import PageLoader from '@/components/PageLoader'
import TagsInput from '@/components/TagsInput'
import { sidecarsService } from '@/services/sidecars'
import { showSnackbar } from '@/utils/snackbar'

const SPLIT_CHARS = [',', ' ']

function errorMessage(error) {
  return error?.response?.data?.message ?? error?.message
}

/**
 * Edits where the reviews of each listener of one sidecar are posted. A
 * listener is the unit, as for guardrails, data masking and the analyzer. The
 * PUT replaces the whole set, so the modal loads it first and sends every
 * listener back.
 */
function SidecarSlackChannelsModal({ sidecar, listeners, onClose }) {
  const [status, setStatus] = useState('loading')
  const [loadError, setLoadError] = useState(null)
  const [saving, setSaving] = useState(false)
  const [byListener, setByListener] = useState({})

  useEffect(() => {
    let current = true
    sidecarsService
      .slackChannels(sidecar.id)
      .then(({ data }) => {
        if (!current) return
        setByListener(Object.fromEntries((data?.listeners ?? []).map((l) => [l.name, l.channels ?? []])))
        setStatus('ready')
      })
      .catch((error) => {
        if (!current) return
        setLoadError(errorMessage(error))
        setStatus('error')
      })
    return () => {
      current = false
    }
  }, [sidecar.id])

  async function handleSave() {
    setSaving(true)
    try {
      await sidecarsService.updateSlackChannels(sidecar.id, {
        listeners: listeners
          .map((l) => ({ name: l.name, channels: byListener[l.name] ?? [] }))
          .filter((l) => l.channels.length > 0),
      })
      showSnackbar({ level: 'success', text: 'Slack channels saved.' })
      onClose()
    } catch (error) {
      showSnackbar({ level: 'error', text: 'Failed to save Slack channels.', description: errorMessage(error) })
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal opened onClose={onClose} title={`Slack channels: ${sidecar.name}`} size="lg">
      {status === 'loading' ? (
        <PageLoader h={160} />
      ) : status === 'error' ? (
        <PageLoader error h={160} message="Failed to load Slack channels." description={loadError} />
      ) : (
        <Stack gap="md">
          <Alert color="blue" variant="light" icon={<Info size={16} />} radius="md">
            <Text size="sm">
              Use channel IDs. Invite the Slack App to private channels. The default channel set in
              Configurations also receives every review.
            </Text>
          </Alert>

          {listeners.length === 0 && (
            <Text size="sm" c="dimmed">
              This sidecar has no named listener.
            </Text>
          )}

          {listeners.map((listener, index) => (
            <TagsInput
              key={listener.name}
              label={`Listener ${listener.name}`}
              description="Reviews this listener holds. Empty sends them to the default channel only."
              placeholder="C031T9LDGAH"
              splitChars={SPLIT_CHARS}
              value={byListener[listener.name] ?? []}
              onChange={(value) => setByListener((prev) => ({ ...prev, [listener.name]: value }))}
              data-autofocus={index === 0 || undefined}
            />
          ))}

          <Group justify="flex-end">
            <Button variant="default" onClick={onClose} disabled={saving}>
              Cancel
            </Button>
            <Button onClick={handleSave} loading={saving}>
              Save
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  )
}

export default SidecarSlackChannelsModal
