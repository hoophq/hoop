import { Anchor, Group, SimpleGrid, Stack, Title } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import Badge from '@/components/Badge'
import { useUserStore } from '@/stores/useUserStore'
import ProfileCard from './ProfileCard'
import AddLicenseModal from './AddLicenseModal'
import { COMPLIANCE_PROFILES, GENERAL_PROFILES, MANUAL_CARD } from './constants'

/**
 * The protection-profile picker shared by the onboarding page and the
 * Settings → Protection rules page: three sections of selectable cards.
 * Enterprise profiles are disabled on the free plan, with an inline
 * "Add your license here" escape hatch.
 *
 * Props:
 * - value:    selected profile id, MANUAL_PROFILE, or null (nothing selected)
 * - onChange: (profileId) => void
 */
function ProfileSelector({ value, onChange }) {
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const [licenseModalOpened, { open: openLicenseModal, close: closeLicenseModal }] =
    useDisclosure(false)

  const isDisabled = (profile) => profile.enterprise && isFreeLicense

  const renderCard = (profile) => (
    <ProfileCard
      key={profile.id}
      profile={profile}
      selected={value === profile.id}
      disabled={isDisabled(profile)}
      onSelect={() => onChange(profile.id)}
    />
  )

  return (
    <Stack gap="xl" role="radiogroup" aria-label="Protection profile">
      <Stack gap="md">
        <Group justify="space-between">
          <Group gap="sm">
            <Title order={3}>Built for compliance</Title>
            <Badge variant="light" color="indigo" size="sm" tt="none">
              Enterprise
            </Badge>
          </Group>
          {isFreeLicense && (
            <Anchor component="button" type="button" size="sm" fw={500} onClick={openLicenseModal}>
              Add your license here
            </Anchor>
          )}
        </Group>
        <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md">
          {COMPLIANCE_PROFILES.map(renderCard)}
        </SimpleGrid>
      </Stack>

      <Stack gap="md">
        <Title order={3}>General purpose protection</Title>
        <SimpleGrid cols={{ base: 1, md: 3 }} spacing="md">
          {GENERAL_PROFILES.map(renderCard)}
        </SimpleGrid>
      </Stack>

      <Stack gap="md">
        <Title order={3}>Start from scratch</Title>
        {renderCard(MANUAL_CARD)}
      </Stack>

      <AddLicenseModal opened={licenseModalOpened} onClose={closeLicenseModal} />
    </Stack>
  )
}

export default ProfileSelector
