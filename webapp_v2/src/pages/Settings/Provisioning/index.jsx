import { useEffect, useState } from 'react'
import { Group, Stack, Text, Title } from '@mantine/core'
import Button from '@/components/Button'
import PageLoader from '@/components/PageLoader'
import SectionRow from '@/components/SectionRow'
import SegmentedControl from '@/components/SegmentedControl'
import { useMinDelay } from '@/hooks/useMinDelay'
import { showSnackbar } from '@/utils/snackbar'
import FileSection from './sections/FileSection'
import SlackSyncSection from './sections/SlackSyncSection'
import { useProvisioningStore } from './store'

const METHODS = [
  { value: 'slack', label: 'Slack' },
  { value: 'file', label: 'File' },
]

/**
 * Where the control plane's reviewers come from (ADR-0019): Slack user groups
 * by default, or a file. The Users page also edits groups while the Slack
 * import does not manage them. Nobody has to log in for a Slack approval to
 * recognize them. SCIM is the next source.
 */
export default function SettingsProvisioning() {
  const status = useProvisioningStore((s) => s.status)
  const sync = useProvisioningStore((s) => s.sync)
  const groupsManaged = useProvisioningStore((s) => s.groupsManaged)
  const saving = useProvisioningStore((s) => s.saving)
  const load = useProvisioningStore((s) => s.load)
  const stopManagingGroups = useProvisioningStore((s) => s.stopManagingGroups)
  const [method, setMethod] = useState('slack')

  useEffect(() => {
    load()
  }, [load])

  const showLoader = useMinDelay(status === 'loading' || status === 'idle')
  if (showLoader) return <PageLoader h={400} />
  if (status === 'error') return <PageLoader error h={400} message="Failed to load provisioning." />

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
          Bring reviewers and their groups from Slack or a file. Reviewers approve in Slack without signing in
          to the control plane.
        </Text>
      </Stack>

      <SegmentedControl data={METHODS} value={method} onChange={setMethod} />

      {method === 'slack' && <SlackSyncSection key={sync?.enabled ? 'on' : 'off'} />}
      {method === 'file' && <FileSection groupsManaged={groupsManaged} />}

      {groupsManaged && (
        <SectionRow
          title="Managed groups"
          description="The Slack import owns the groups: login and the Users page change only the admin group."
        >
          <Group justify="space-between" align="center">
            <Text size="sm" c="dimmed" maw={420}>
              {sync?.enabled
                ? 'Remove the Slack import first; its next run would take the groups back.'
                : 'Hand the groups back to login and the Users page. Users and their groups stay.'}
            </Text>
            <Button variant="outline" color="red" onClick={handleStopManaging} loading={saving} disabled={!!sync?.enabled}>
              Stop managing groups
            </Button>
          </Group>
        </SectionRow>
      )}
    </Stack>
  )
}
