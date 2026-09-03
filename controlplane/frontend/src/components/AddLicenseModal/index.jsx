import { useState } from 'react'
import { Anchor, Group, Stack, Text } from '@mantine/core'
import { ExternalLink } from 'lucide-react'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import Textarea from '@/components/Textarea'
import { MODAL_COPY } from '@/features/License/constants'
import { useLicenseUpdate } from '@/features/License/useLicenseUpdate'
import { useUIStore } from '@/stores/useUIStore'
import { useUserStore } from '@/stores/useUserStore'
import { docsUrl } from '@/utils/docsUrl'
import { LICENSE_STATE, licenseState } from '@/utils/license'
import { openSales } from '@/utils/support'

/**
 * The "Add your license" dialog. Mounted once in layout/Layout.jsx and opened
 * through useUIStore.openLicenseModal(), so every surface shares one copy.
 *
 * Ported from webapp_v2's ProtectionProfiles/AddLicenseModal with one save path
 * (features/License/useLicenseUpdate) and no disabled state: the server allows
 * replacing an installed license, and renewal needs exactly that.
 */
export default function AddLicenseModal() {
  const opened = useUIStore((state) => state.licenseModalOpened)
  const closeLicenseModal = useUIStore((state) => state.closeLicenseModal)
  const licenseInfo = useUserStore((state) => state.licenseInfo)
  const { save, saving } = useLicenseUpdate()
  const [licenseKey, setLicenseKey] = useState('')

  const state = licenseState(licenseInfo)
  const hasDocument = state !== LICENSE_STATE.FREE && state !== LICENSE_STATE.UNKNOWN
  const copy = hasDocument ? MODAL_COPY.update : MODAL_COPY.free

  function handleClose() {
    setLicenseKey('')
    closeLicenseModal()
  }

  async function handleSave() {
    const { ok } = await save(licenseKey)
    if (ok) handleClose()
  }

  return (
    <Modal opened={opened} onClose={handleClose} title={copy.title} size="lg">
      <Stack gap="md">
        <Stack gap={4}>
          <Text size="md">{copy.lead}</Text>
          <Text size="sm" c="dimmed">
            {copy.detail}
          </Text>
        </Stack>

        {state === LICENSE_STATE.INVALID && licenseInfo?.verify_error && (
          <Text size="sm" c="red">
            {`Current license: ${licenseInfo.verify_error}`}
          </Text>
        )}

        <Textarea
          label="License key"
          placeholder="Paste the license JSON here"
          value={licenseKey}
          onChange={(event) => setLicenseKey(event.currentTarget.value)}
          minRows={6}
          maxRows={14}
          spellCheck={false}
        />

        <Group justify="space-between" align="center" wrap="wrap">
          <Anchor
            href={docsUrl.setup.licenseManagement}
            target="_blank"
            rel="noopener noreferrer"
            size="xs"
            c="dimmed"
          >
            License management documentation
          </Anchor>
          <Group gap="md">
            <Button variant="subtle" color="gray" onClick={handleClose}>
              Cancel
            </Button>
            <Button
              variant="default"
              rightSection={<ExternalLink size={14} />}
              onClick={() => openSales()}
            >
              Talk to sales
            </Button>
            <Button loading={saving} disabled={!licenseKey.trim()} onClick={handleSave}>
              Save
            </Button>
          </Group>
        </Group>
      </Stack>
    </Modal>
  )
}
