import { useEffect } from 'react'
import { Stack, Text, Title } from '@mantine/core'
import PageLoader from '@/components/PageLoader'
import { useMinDelay } from '@/hooks/useMinDelay'
import SlackSyncSection from './sections/SlackSyncSection'
import { useProvisioningStore } from './store'

/**
 * Where the control plane's reviewers come from (ADR-0019): Slack user groups.
 * The Users page also edits groups. Nobody has to log in for a Slack approval
 * to recognize them. SCIM is the next source.
 */
export default function SettingsProvisioning() {
  const status = useProvisioningStore((s) => s.status)
  const sync = useProvisioningStore((s) => s.sync)
  const load = useProvisioningStore((s) => s.load)

  useEffect(() => {
    load()
  }, [load])

  const showLoader = useMinDelay(status === 'loading' || status === 'idle')
  if (showLoader) return <PageLoader h={400} />
  if (status === 'error') return <PageLoader error h={400} message="Failed to load provisioning." />

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={1}>Provisioning</Title>
        <Text c="dimmed">
          Bring reviewers and their groups from Slack user groups. Reviewers approve in Slack without signing
          in to the control plane.
        </Text>
      </Stack>

      <SlackSyncSection key={sync?.enabled ? 'on' : 'off'} />
    </Stack>
  )
}
