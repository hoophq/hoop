import { useState } from 'react'
import { Group, Stack, Text } from '@mantine/core'
import { ExternalLink } from 'lucide-react'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import Textarea from '@/components/Textarea'
import licenseService from '@/services/license'
import { authService } from '@/services/auth'
import { useUserStore } from '@/stores/useUserStore'
import { showSnackbar } from '@/utils/snackbar'

const SALES_URL = 'https://hoop.dev/meet'
const INTERCOM_MESSAGE = 'I want to upgrade my current plan'

/**
 * "Add your license" dialog shown from the protection-rules pages when the
 * organization is on the free plan. Saving a valid enterprise license
 * refreshes the server info so the Enterprise profile cards unlock in place.
 */
function AddLicenseModal({ opened, onClose }) {
  const { analyticsTracking, setServerInfo } = useUserStore()
  const [licenseKey, setLicenseKey] = useState('')
  const [saving, setSaving] = useState(false)

  function handleClose() {
    setLicenseKey('')
    onClose()
  }

  function handleTalkToSales() {
    if (analyticsTracking && window.Intercom) {
      window.Intercom('showNewMessage', INTERCOM_MESSAGE)
      return
    }
    window.open(SALES_URL, '_blank', 'noopener,noreferrer')
  }

  async function handleSave() {
    let parsed
    try {
      parsed = JSON.parse(licenseKey)
    } catch {
      showSnackbar({ level: 'error', text: 'Error processing license: invalid JSON format' })
      return
    }

    setSaving(true)
    try {
      await licenseService.update(parsed)
    } catch (err) {
      showSnackbar({
        level: 'error',
        text: 'Failed to update license',
        description: err.response?.data?.message,
      })
      setSaving(false)
      return
    }

    // The license is active server-side from here on — a failure below only
    // means the in-memory server info could not be refreshed.
    try {
      const serverInfo = await authService.getServerInfo()
      setServerInfo(serverInfo)
      showSnackbar({ level: 'success', text: 'License updated successfully' })
    } catch {
      showSnackbar({
        level: 'info',
        text: 'License updated successfully',
        description: 'Reload the page to unlock Enterprise features.',
      })
    } finally {
      setSaving(false)
      handleClose()
    }
  }

  return (
    <Modal opened={opened} onClose={handleClose} title="Add your license" size="lg">
      <Stack gap="md">
        <Stack gap={4}>
          <Text size="md">Get the most out of Hoop with our Enterprise Plan.</Text>
          <Text size="sm" c="dimmed">
            If you don't have one, reach out to us.
          </Text>
        </Stack>

        <Textarea
          label="License key"
          placeholder="Paste your license key here"
          value={licenseKey}
          onChange={(e) => setLicenseKey(e.currentTarget.value)}
          minRows={4}
        />

        <Group justify="flex-end" gap="md">
          <Button variant="subtle" color="gray" onClick={handleClose}>
            Cancel
          </Button>
          <Button
            variant="default"
            rightSection={<ExternalLink size={14} />}
            onClick={handleTalkToSales}
          >
            Talk to sales
          </Button>
          <Button loading={saving} disabled={!licenseKey.trim()} onClick={handleSave}>
            Save
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}

export default AddLicenseModal
