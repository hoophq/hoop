import { useState } from 'react'
import { Navigate, useNavigate } from 'react-router-dom'
import { Box, Group, SimpleGrid, Stack, Text, Title } from '@mantine/core'
import Button from '@/components/Button'
import PasswordInput from '@/components/PasswordInput'
import { useUserStore } from '@/stores/useUserStore'
import { skipLicenseIntro } from '@/utils/licenseIntro'
import classes from './License.module.css'

const SIDECARS_PATH = '/sidecars'

/**
 * The control plane's first-access screen (Figma: Control-Plane-UI, "First Time
 * - Option B"). ControlPlaneProtectedRoute sends an admin on the free plan with
 * no sidecar here; Confirm installs an Enterprise license, "I don't have a
 * license" remembers the skip for this user and goes on to Sidecars.
 *
 * Chrome-less like the other onboarding routes. Same save sequence as
 * features/ProtectionProfiles/AddLicenseModal.
 */
function LicenseIntro() {
  const navigate = useNavigate()
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const userId = useUserStore((s) => s.user?.id)
  const installLicense = useUserStore((s) => s.installLicense)

  const [licenseKey, setLicenseKey] = useState('')
  const [error, setError] = useState(null)
  const [saving, setSaving] = useState(false)

  // A bookmark after licensing must not strand anyone here.
  if (!isFreeLicense) return <Navigate to={SIDECARS_PATH} replace />

  const handleSkip = () => {
    // Written before navigating: /sidecars mounts a new gate that runs onReady
    // again and reads this key.
    skipLicenseIntro(userId)
    navigate(SIDECARS_PATH, { replace: true })
  }

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (saving) return
    setError(null)

    let parsed
    try {
      parsed = JSON.parse(licenseKey)
    } catch {
      setError('The license token is not valid JSON. Paste the whole document you received.')
      return
    }

    setSaving(true)
    const result = await installLicense(parsed)

    if (!result.ok) {
      setError(result.message)
      setSaving(false)
      return
    }
    // The license is live but the store could not be refreshed. A full reload
    // fetches /serverinfo instead of navigating on stale state.
    if (result.needsReload) {
      window.location.replace(SIDECARS_PATH)
      return
    }
    setSaving(false)

    // Installed and the store is current, so this reads the new plan.
    if (useUserStore.getState().isFreeLicense) {
      setError('This license is valid, but it is not an Enterprise license.')
      return
    }
    navigate(SIDECARS_PATH, { replace: true })
  }

  return (
    <SimpleGrid cols={{ base: 1, md: 2 }} spacing={0} mih="100vh" className={classes.shell}>
      <Box component="aside" aria-hidden="true" className={classes.leftPanel} visibleFrom="md" />

      <Box className={classes.rightPanel}>
        <Box className={classes.content}>
          <img
            src="/images/hoop-branding/SVG/hoop-symbol_black.svg"
            alt="hoop.dev"
            width={64}
            height={64}
            className={classes.logo}
          />

          <Stack gap="xs" mt="xl" mb="xl">
            <Title order={1} className={classes.heading}>
              License
            </Title>
            <Text size="md">Insert the license token you have received to activate your Control Plane</Text>
          </Stack>

          <form onSubmit={handleSubmit}>
            <Stack gap="xl">
              <PasswordInput
                aria-label="License token"
                placeholder="License Token"
                value={licenseKey}
                onChange={(e) => {
                  setLicenseKey(e.currentTarget.value)
                  if (error) setError(null)
                }}
                error={error}
                autoComplete="off"
                required
              />

              <Group gap="md">
                <Button type="submit" loading={saving} disabled={!licenseKey.trim()}>
                  Confirm
                </Button>
                <Button variant="subtle" onClick={handleSkip} disabled={saving}>
                  {"I don't have a license"}
                </Button>
              </Group>
            </Stack>
          </form>
        </Box>
      </Box>
    </SimpleGrid>
  )
}

export default LicenseIntro
