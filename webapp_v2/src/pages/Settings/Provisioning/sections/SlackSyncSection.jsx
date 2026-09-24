import { useEffect, useMemo, useState } from 'react'
import { Group, Stack, Text } from '@mantine/core'
import { TriangleAlert } from 'lucide-react'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import Code from '@/components/Code'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import SectionRow from '@/components/SectionRow'
import Switch from '@/components/Switch'
import { showSnackbar } from '@/utils/snackbar'
import { useProvisioningStore } from '../store'

function formatDate(value) {
  return value ? new Date(value).toLocaleString() : 'never'
}

/**
 * Slack import: the members of the Slack user groups an admin picks become
 * hoop users in hoop groups named after the group handle. It reads through the
 * org's Slack app, so there is nothing to configure but the groups.
 */
export default function SlackSyncSection() {
  const sync = useProvisioningStore((s) => s.sync)
  const groups = useProvisioningStore((s) => s.groups)
  const groupsStatus = useProvisioningStore((s) => s.groupsStatus)
  const groupsError = useProvisioningStore((s) => s.groupsError)
  const saving = useProvisioningStore((s) => s.saving)
  const running = useProvisioningStore((s) => s.running)
  const saveSync = useProvisioningStore((s) => s.saveSync)
  const deleteSync = useProvisioningStore((s) => s.deleteSync)
  const runSync = useProvisioningStore((s) => s.runSync)
  const loadGroups = useProvisioningStore((s) => s.loadGroups)

  const [groupIds, setGroupIds] = useState(sync?.group_ids ?? [])
  const [intervalMinutes, setIntervalMinutes] = useState(sync?.interval_minutes ?? 15)
  const [allowMemberManaged, setAllowMemberManaged] = useState(!!sync?.allow_member_managed_groups)

  useEffect(() => {
    if (groupsStatus === 'idle') loadGroups()
  }, [groupsStatus, loadGroups])

  const groupOptions = useMemo(() => {
    const known = groups.map((g) => ({
      value: g.id,
      label: g.admin_managed ? `@${g.name}` : `@${g.name} (edited by a member)`,
    }))
    const missing = groupIds
      .filter((id) => !groups.some((g) => g.id === id))
      .map((id) => ({ value: id, label: id }))
    return [...known, ...missing]
  }, [groups, groupIds])

  const memberManagedPicked = groups.some((g) => groupIds.includes(g.id) && !g.admin_managed)

  async function handleSave() {
    const { ok, error } = await saveSync({
      group_ids: groupIds,
      interval_minutes: Number(intervalMinutes) || 15,
      allow_member_managed_groups: allowMemberManaged,
    })
    if (ok) showSnackbar({ level: 'success', text: 'Slack import saved.' })
    else showSnackbar({ level: 'error', text: 'Failed to save the Slack import.', description: error })
  }

  async function handleRun() {
    const { ok, error } = await runSync()
    if (ok) showSnackbar({ level: 'success', text: 'Slack import finished.' })
    else showSnackbar({ level: 'error', text: 'The Slack import failed.', description: error })
  }

  async function handleDelete() {
    const { ok, error } = await deleteSync()
    if (ok) showSnackbar({ level: 'success', text: 'Slack import removed. Imported users and groups stay, and SSO login manages groups again.' })
    else showSnackbar({ level: 'error', text: 'Failed to remove the Slack import.', description: error })
  }

  return (
    <Stack gap="xl">
      <Alert color="yellow" variant="light" icon={<TriangleAlert size={16} />} radius="md">
        <Text size="sm">
          hoop cannot restrict who edits user groups; restrict it to admins in Slack workspace settings.
          A group last edited by a member who is not an admin is refused unless you allow it below.
        </Text>
      </Alert>

      <SectionRow
        title="User groups"
        badge={
          <Badge variant={sync?.enabled ? 'active' : 'inactive'}>{sync?.enabled ? 'Enabled' : 'Disabled'}</Badge>
        }
        description="Members of these Slack user groups become hoop users. The group handle is the hoop group name: @dba-leads is the group dba-leads, which you name as reviewers on a rule. A handle renamed in Slack keeps its first name here."
      >
        <Stack gap="md">
          <Text size="sm" c="dimmed">
            {'The Slack app needs the '}
            <Code>users:read</Code>
            {', '}
            <Code>users:read.email</Code>
            {' and '}
            <Code>usergroups:read</Code>
            {' scopes.'}
          </Text>
          <MultiSelect
            label="User groups to import"
            placeholder={groupsStatus === 'loading' ? 'Loading user groups…' : 'Select user groups'}
            data={groupOptions}
            value={groupIds}
            onChange={setGroupIds}
            searchable
          />
          {groupsError && (
            <Group gap="sm">
              <Text size="sm" c="red">
                {groupsError}
              </Text>
              <Button variant="subtle" size="xs" onClick={loadGroups}>
                Retry
              </Button>
            </Group>
          )}
          <Switch
            label="Allow member-managed groups"
            description="Accept user groups last edited by a workspace member who is not an admin or owner. Anyone who can edit such a group can make themselves a reviewer."
            checked={allowMemberManaged}
            onChange={(e) => setAllowMemberManaged(e.currentTarget.checked)}
          />
          {memberManagedPicked && !allowMemberManaged && (
            <Text size="sm" c="red">
              A selected group was last edited by a member. The import refuses it until you allow member-managed
              groups or an admin edits it in Slack.
            </Text>
          )}
          <NumberInput
            label="Minutes between runs"
            min={5}
            value={intervalMinutes}
            onChange={setIntervalMinutes}
          />
        </Stack>
      </SectionRow>

      {sync?.enabled && (
        <SectionRow title="Last run" description="Users who leave every selected group keep their account and lose the imported groups. A user deactivated in Slack is deactivated here, except administrators.">
          <Text size="sm" c={sync.last_error ? 'red' : 'dimmed'}>
            {sync.last_run_at
              ? `${formatDate(sync.last_run_at)}: ${sync.last_error || 'Succeeded.'}`
              : 'Not run yet.'}
          </Text>
        </SectionRow>
      )}

      <Group justify="flex-end" gap="sm">
        {sync?.enabled && (
          <>
            <Button variant="outline" color="red" onClick={handleDelete} loading={saving}>
              Remove
            </Button>
            <Button variant="outline" onClick={handleRun} loading={running} disabled={!sync.group_ids?.length}>
              Sync now
            </Button>
          </>
        )}
        <Button onClick={handleSave} loading={saving}>
          Save
        </Button>
      </Group>
    </Stack>
  )
}
