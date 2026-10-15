import { Anchor, Group, Text } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { Info } from 'lucide-react'
import Alert from '@/components/Alert'
import AddLicenseModal from '@/features/ProtectionProfiles/AddLicenseModal'
import { useUserStore } from '@/stores/useUserStore'

// The free plan's sidecar seats; gateway/services/sidecarseats.go holds the
// number the API enforces.
export const FREE_SIDECAR_SEATS = 1

const MESSAGE =
  'Free plan: 1 sidecar, with 1 guardrail, 1 data masking and 1 access request rule. ' +
  'A license lifts these limits.'

/**
 * Free-plan callout on the sidecars page, with the same "Add your license here"
 * escape hatch the protection-profile picker uses. Hidden once a valid
 * Enterprise license is installed.
 */
export default function SidecarLicenseNotice() {
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const [licenseModalOpened, { open: openLicenseModal, close: closeLicenseModal }] =
    useDisclosure(false)

  if (!isFreeLicense) return null

  return (
    <>
      <Alert color="blue" variant="light" icon={<Info size={16} />} radius="md">
        <Group gap="xs" align="center" wrap="wrap">
          <Text size="sm" component="span">
            {MESSAGE}
          </Text>
          <Anchor component="button" type="button" size="sm" fw={500} onClick={openLicenseModal}>
            Add your license here
          </Anchor>
        </Group>
      </Alert>

      <AddLicenseModal opened={licenseModalOpened} onClose={closeLicenseModal} />
    </>
  )
}
