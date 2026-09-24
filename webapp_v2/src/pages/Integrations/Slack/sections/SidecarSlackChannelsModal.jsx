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
 * Edits where one sidecar's reviews are posted. The PUT replaces the whole
 * set, so the modal loads it first and sends every field back. An empty
 * listener field inherits the sidecar's channels.
 */
function SidecarSlackChannelsModal({ sidecar, listeners, onClose }) {
  const [status, setStatus] = useState('loading')
  const [loadError, setLoadError] = useState(null)
  const [saving, setSaving] = useState(false)
  const [channels, setChannels] = useState([])
  const [byListener, setByListener] = useState({})

  useEffect(() => {
    let current = true
    sidecarsService
      .slackChannels(sidecar.id)
      .then(({ data }) => {
        if (!current) return
        setChannels(data?.channels ?? [])
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
        channels,
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

          <TagsInput
            label="Sidecar channels"
            description="Reviews from every listener of this sidecar."
            placeholder="C039AQNN5DF"
            splitChars={SPLIT_CHARS}
            value={channels}
            onChange={setChannels}
            data-autofocus
          />

          {listeners.map((listener) => (
            <TagsInput
              key={listener.name}
              label={`Listener ${listener.name}`}
              description="Replaces the sidecar channels for this listener. Empty inherits them."
              placeholder="C031T9LDGAH"
              splitChars={SPLIT_CHARS}
              value={byListener[listener.name] ?? []}
              onChange={(value) => setByListener((prev) => ({ ...prev, [listener.name]: value }))}
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
