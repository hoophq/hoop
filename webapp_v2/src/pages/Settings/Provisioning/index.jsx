import { useEffect, useState } from 'react'
import { Group, Stack, Text, Title } from '@mantine/core'
import { Info } from 'lucide-react'
import Alert from '@/components/Alert'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import SectionRow from '@/components/SectionRow'
import SegmentedControl from '@/components/SegmentedControl'
import { useMinDelay } from '@/hooks/useMinDelay'
import { showSnackbar } from '@/utils/snackbar'
import FileSection from './sections/FileSection'
import ScimSection from './sections/ScimSection'
import SlackSyncSection from './sections/SlackSyncSection'
import { useProvisioningStore } from './store'

const METHODS = [
  { value: 'slack', label: 'Slack' },
  { value: 'scim', label: 'SCIM' },
  { value: 'file', label: 'File' },
]

const LABELS = { slack: 'The Slack import', scim: 'SCIM' }

/**
 * Where the control plane's reviewers come from (ADR-0019), in order of
 * preference: Slack user groups, SCIM from the identity provider, or a file.
 * The Users page also edits groups while no source manages them. Nobody has
 * to log in for a Slack approval to recognize them.
 */
export default function SettingsProvisioning() {
  const status = useProvisioningStore((s) => s.status)
  const scim = useProvisioningStore((s) => s.scim)
  const sync = useProvisioningStore((s) => s.sync)
  const groupsManaged = useProvisioningStore((s) => s.groupsManaged)
  const saving = useProvisioningStore((s) => s.saving)
  const load = useProvisioningStore((s) => s.load)
  const stopManagingGroups = useProvisioningStore((s) => s.stopManagingGroups)
  const [method, setMethod] = useState(null)

  useEffect(() => {
    load()
  }, [load])

  const showLoader = useMinDelay(status === 'loading' || status === 'idle')
  if (showLoader) return <PageLoader h={400} />
  if (status === 'error') return <PageLoader error h={400} message="Failed to load provisioning." />

  const active = sync?.enabled ? 'slack' : scim?.enabled ? 'scim' : null
  const current = method ?? active ?? 'slack'
  const otherActive = active !== null && current !== 'file' && active !== current

  async function handleStopManaging() {
    const { ok, error } = await stopManagingGroups()
    if (ok) showSnackbar({ level: 'success', text: 'Groups are no longer managed. Login and the Users page own them.' })
    else showSnackbar({ level: 'error', text: 'Failed to stop managing groups.', description: error })
  }

  return (
    <Stack gap="xl">
      <Stack gap="xs">
        <Title order={1}>Provisioning</Title>
        <Text c="dimmed">
          Bring reviewers and their groups from Slack, your identity provider or a file. Reviewers approve in
          Slack without signing in to the control plane.
        </Text>
      </Stack>

      <SegmentedControl data={METHODS} value={current} onChange={setMethod} />

      {otherActive && (
        <Alert color="blue" variant="light" icon={<Info size={16} />} radius="md">
          <Text size="sm">
            {`${LABELS[active]} is active. Remove it before enabling ${current === 'scim' ? 'SCIM' : 'the Slack import'}.`}
          </Text>
        </Alert>
      )}

      {current === 'slack' && <SlackSyncSection key={sync?.enabled ? 'on' : 'off'} disabled={otherActive} />}
      {current === 'scim' && <ScimSection disabled={otherActive} />}
      {current === 'file' && <FileSection groupsManaged={groupsManaged} />}

      {groupsManaged && (
        <SectionRow
          title="Managed groups"
          description="The Slack import or SCIM owns the groups: login and the Users page change only the admin group."
        >
          <Group justify="space-between" align="center">
            <Text size="sm" c="dimmed" maw={420}>
              {active
                ? 'Remove the Slack import and the SCIM token first; they would take the groups back.'
                : 'Hand the groups back to login and the Users page. Users and their groups stay.'}
            </Text>
            <Button variant="outline" color="red" onClick={handleStopManaging} loading={saving} disabled={!!active}>
              Stop managing groups
            </Button>
          </Group>
        </SectionRow>
      )}
    </Stack>
  )
}
