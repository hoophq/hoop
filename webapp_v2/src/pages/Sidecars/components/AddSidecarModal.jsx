import { Group, Stack, Text } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { Info } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import Modal from '@/components/Modal'
import AddLicenseModal from '@/features/ProtectionProfiles/AddLicenseModal'
import { useUserStore } from '@/stores/useUserStore'
import SidecarMethodCards from './SidecarMethodCards'

// "Add new Sidecar" from a filled list (Figma: "Connect a Sidecar" modal). On
// the free plan it states the per-sidecar rule caps the sidecar enforces
// (sidecar/daemon/limits.go: one masking rule, one guardrail) and offers the
// license dialog.
export default function AddSidecarModal({ opened, onClose }) {
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const [licenseOpened, { open: openLicense, close: closeLicense }] = useDisclosure(false)

  return (
    <>
      <Modal opened={opened} onClose={onClose} title="Connect a Sidecar" size="lg">
        <Stack gap="lg">
          <SidecarMethodCards onPick={onClose} />
          {isFreeLicense && (
            <Alert color="blue" variant="light" radius="md" icon={<Info size={16} />}>
              <Group justify="space-between" align="center" wrap="nowrap" gap="md">
                <Text size="sm">
                  <Text component="span" fw={700}>
                    Free tier:
                  </Text>
                  {' 1 Data Masking rule and 1 Guardrail per sidecar. Extra rules stay active until you upgrade.'}
                </Text>
                <Button size="xs" variant="light" onClick={openLicense} flex="0 0 auto">
                  Upgrade
                </Button>
              </Group>
            </Alert>
          )}
        </Stack>
      </Modal>
      <AddLicenseModal opened={licenseOpened} onClose={closeLicense} />
    </>
  )
}
