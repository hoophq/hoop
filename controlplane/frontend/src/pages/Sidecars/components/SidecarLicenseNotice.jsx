import { useState } from 'react'
import { Box, Group, Stack, Text, Title } from '@mantine/core'
import { AlertCircle, ExternalLink, RefreshCw } from 'lucide-react'
import AddLicenseCta from '@/components/AddLicenseCta'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import {
  SIDECAR_AUTO_ACTIVATION_COPY,
  SIDECAR_AUTO_ACTIVATION_SHIPPED,
  SIDECAR_NOTICE_COPY,
} from '@/features/License/constants'
import { useUserStore } from '@/stores/useUserStore'
import { docsUrl } from '@/utils/docsUrl'
import { LICENSE_STATE, licenseState } from '@/utils/license'
import { openSales } from '@/utils/support'

/**
 * License-aware section for the two sidecar journeys: create/deploy a new
 * sidecar and connect an existing one. Renders nothing while the organization
 * holds a valid Enterprise license, so the future pages wrap their forms in it
 * and proceed. `journey` ('create' | 'connect') picks the one-line explanation.
 *
 * "Check again" re-reads /serverinfo: a license installed from the CLI, or one
 * a sidecar will deliver once that path ships, shows up without a reload.
 */
export default function SidecarLicenseNotice({ journey }) {
  const licenseInfo = useUserStore((state) => state.licenseInfo)
  const serverInfoLoaded = useUserStore((state) => state.serverInfoLoaded)
  const refreshServerInfo = useUserStore((state) => state.refreshServerInfo)
  const [checking, setChecking] = useState(false)

  const state = licenseState(licenseInfo, serverInfoLoaded)
  if (state === LICENSE_STATE.ACTIVE || state === LICENSE_STATE.UNKNOWN) return null

  if (state !== LICENSE_STATE.FREE) {
    // The shell banner already says why. One line and the way out.
    return (
      <Alert color="red" variant="light" radius="md" icon={<AlertCircle size={16} />}>
        <Group gap="xs" align="center" wrap="wrap">
          <Text size="sm" component="span">
            {SIDECAR_NOTICE_COPY.renewal}
          </Text>
          <AddLicenseCta variant="anchor" label="Update license" c="red" />
        </Group>
      </Alert>
    )
  }

  async function checkAgain() {
    setChecking(true)
    await refreshServerInfo()
    setChecking(false)
  }

  return (
    <Box bd="1px solid var(--mantine-color-default-border)" bdrs="md" p="lg">
      <Stack gap="md">
        <Stack gap={4}>
          <Title order={3}>{SIDECAR_NOTICE_COPY.title}</Title>
          <Text size="sm" c="dimmed">
            {SIDECAR_NOTICE_COPY.body}
          </Text>
          {journey && (
            <Text size="sm" c="dimmed">
              {SIDECAR_NOTICE_COPY.journeys[journey]}
            </Text>
          )}
          {SIDECAR_AUTO_ACTIVATION_SHIPPED && (
            <Text size="sm" c="dimmed">
              {SIDECAR_AUTO_ACTIVATION_COPY}
            </Text>
          )}
          <Text size="sm" c="dimmed">
            {SIDECAR_NOTICE_COPY.footer}
          </Text>
        </Stack>
        <Group gap="md" wrap="wrap">
          <AddLicenseCta />
          <Button
            variant="default"
            rightSection={<ExternalLink size={14} />}
            onClick={() => openSales()}
          >
            Talk to sales
          </Button>
          <Button
            variant="subtle"
            color="gray"
            leftSection={<RefreshCw size={14} />}
            loading={checking}
            onClick={checkAgain}
          >
            Check again
          </Button>
        </Group>
        <DocsBtnCallOut
          text="License management documentation"
          href={docsUrl.setup.licenseManagement}
        />
      </Stack>
    </Box>
  )
}
