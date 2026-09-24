import { useEffect, useState } from 'react'
import { Stack, Text, Title } from '@mantine/core'
import { Info } from 'lucide-react'
import Alert from '@/components/Alert'
import PageLoader from '@/components/PageLoader'
import SegmentedControl from '@/components/SegmentedControl'
import { useMinDelay } from '@/hooks/useMinDelay'
import DirectorySyncSection from './sections/DirectorySyncSection'
import ScimSection from './sections/ScimSection'
import { useProvisioningStore } from './store'

const METHODS = [
  { value: 'scim', label: 'SCIM' },
  { value: 'sync', label: 'Directory sync' },
]

/**
 * Where the control plane's reviewers come from (ADR-0019). The identity
 * provider is the only source of users and groups: it pushes them over SCIM,
 * or the control plane pulls them from Google Workspace, Auth0 or Cognito.
 * Nobody has to log in for a Slack approval to recognize them.
 */
export default function SettingsProvisioning() {
  const status = useProvisioningStore((s) => s.status)
  const scim = useProvisioningStore((s) => s.scim)
  const sync = useProvisioningStore((s) => s.sync)
  const load = useProvisioningStore((s) => s.load)
  const [method, setMethod] = useState(null)

  useEffect(() => {
    load()
  }, [load])

  const showLoader = useMinDelay(status === 'loading' || status === 'idle')
  if (showLoader) return <PageLoader h={400} />
  if (status === 'error') return <PageLoader error h={400} message="Failed to load provisioning." />

  const active = sync?.enabled ? 'sync' : scim?.enabled ? 'scim' : null
  const current = method ?? active ?? 'scim'
  const otherActive = active !== null && active !== current

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={1}>Provisioning</Title>
        <Text c="dimmed">
          Bring users and groups from your identity provider. Reviewers approve in Slack without signing
          in to the control plane.
        </Text>
      </Stack>

      <SegmentedControl data={METHODS} value={current} onChange={setMethod} />

      {otherActive && (
        <Alert color="blue" variant="light" icon={<Info size={16} />} radius="md">
          <Text size="sm">
            {`${active === 'scim' ? 'SCIM' : 'Directory sync'} is active. Remove it before enabling ${current === 'scim' ? 'SCIM' : 'directory sync'}.`}
          </Text>
        </Alert>
      )}

      {current === 'scim' ? (
        <ScimSection disabled={otherActive} />
      ) : (
        <DirectorySyncSection key={sync?.provider ?? 'none'} disabled={otherActive} />
      )}
    </Stack>
  )
}
