import { useEffect, useState } from 'react'
import { Group, Stack, Text, Title } from '@mantine/core'
import Button from '@/components/Button'
import { useMinDelay } from '@/hooks/useMinDelay'
import PageLoader from '@/components/PageLoader'
import ProfileSelector from '@/features/ProtectionProfiles/ProfileSelector'
import { fromApiProfile, toApiProfile } from '@/features/ProtectionProfiles/constants'
import { protectionProfilesService } from '@/services/protectionProfiles'
import { showSnackbar } from '@/utils/snackbar'

function SettingsProtectionRules() {
  const [selected, setSelected] = useState(null)
  const [currentProfile, setCurrentProfile] = useState(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)

  const showLoader = useMinDelay(loading)

  useEffect(() => {
    protectionProfilesService
      .get()
      .then(({ profile }) => {
        setCurrentProfile(fromApiProfile(profile))
        setSelected(fromApiProfile(profile))
      })
      .catch((err) =>
        showSnackbar({
          level: 'error',
          text: 'Failed to load protection rules',
          description: err.response?.data?.message,
        })
      )
      .finally(() => setLoading(false))
  }, [])

  async function handleSave() {
    setSaving(true)
    try {
      const { profile } = await protectionProfilesService.update({
        profile: toApiProfile(selected),
        source: 'settings',
      })
      setCurrentProfile(fromApiProfile(profile))
      setSelected(fromApiProfile(profile))
      showSnackbar({ level: 'success', text: 'Protection rules updated.' })
    } catch (err) {
      showSnackbar({
        level: 'error',
        text: 'Failed to update protection rules',
        description: err.response?.data?.message,
      })
    } finally {
      setSaving(false)
    }
  }

  if (showLoader) return <PageLoader h={400} />

  return (
    <Stack gap={0}>
      <Group justify="space-between" align="flex-start" mb="xxxlAlt">
        <Stack gap="xs">
          <Title order={1}>Protection rules</Title>
          <Text size="lg" c="dimmed">
            Pick how Hoop should guard everything you connect.
          </Text>
        </Stack>
        <Button loading={saving} disabled={selected === currentProfile} onClick={handleSave}>
          Save
        </Button>
      </Group>

      <ProfileSelector value={selected} onChange={setSelected} />
    </Stack>
  )
}

export default SettingsProtectionRules
