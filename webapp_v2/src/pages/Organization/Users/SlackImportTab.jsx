import { useEffect, useMemo, useState } from 'react'
import { Group, Stack, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import DocsBtnCallOut from '@/components/DocsBtnCallOut'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import PageLoader from '@/components/PageLoader'
import SectionRow from '@/components/SectionRow'
import { useMinDelay } from '@/hooks/useMinDelay'
import { docsUrl } from '@/utils/docsUrl'
import { showSnackbar } from '@/utils/snackbar'
import { useSlackImportStore } from './slackImportStore'

function formatDate(value) {
  return new Date(value).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}

// Slack answers a missing scope with a code, not a sentence.
function groupsErrorText(error) {
  return error?.includes('missing_scope')
    ? 'The Slack app is missing a scope to read user groups. See the docs.'
    : error
}

function LastRun({ sync }) {
  if (!sync?.last_run_at) {
    return (
      <Text size="xs" c="dimmed">
        Not synced yet.
      </Text>
    )
  }
  if (sync.last_error) {
    return (
      <Text size="xs" c="red" lineClamp={3}>
        {`Last sync failed ${formatDate(sync.last_run_at)}: ${sync.last_error}`}
      </Text>
    )
  }
  return (
    <Text size="xs" c="dimmed">
      {`Last sync ${formatDate(sync.last_run_at)}`}
    </Text>
  )
}

/**
 * The Slack import (ADR-0020): the members of the Slack user groups an admin
 * picks become users, in groups named after the handle. The page says the
 * least it can; the docs carry the rest.
 */
function SlackImportForm({ sync, onSynced }) {
  const groups = useSlackImportStore((s) => s.groups)
  const groupsStatus = useSlackImportStore((s) => s.groupsStatus)
  const groupsError = useSlackImportStore((s) => s.groupsError)
  const saving = useSlackImportStore((s) => s.saving)
  const running = useSlackImportStore((s) => s.running)
  const saveSync = useSlackImportStore((s) => s.saveSync)
  const deleteSync = useSlackImportStore((s) => s.deleteSync)
  const runSync = useSlackImportStore((s) => s.runSync)
  const loadGroups = useSlackImportStore((s) => s.loadGroups)

  const [groupIds, setGroupIds] = useState(sync?.group_ids ?? [])
  const [intervalMinutes, setIntervalMinutes] = useState(sync?.interval_minutes ?? 15)

  useEffect(() => {
    if (groupsStatus === 'idle') loadGroups()
  }, [groupsStatus, loadGroups])

  const groupOptions = useMemo(() => {
    const known = groups.map((g) => ({ value: g.id, label: `@${g.name}` }))
    const missing = groupIds
      .filter((id) => !groups.some((g) => g.id === id))
      .map((id) => ({ value: id, label: id }))
    return [...known, ...missing]
  }, [groups, groupIds])

  const enabled = !!sync?.enabled

  async function handleSave() {
    const { ok, error } = await saveSync({
      group_ids: groupIds,
      interval_minutes: Number(intervalMinutes) || 15,
    })
    if (ok) showSnackbar({ level: 'success', text: 'Slack import saved.' })
    else showSnackbar({ level: 'error', text: 'Failed to save the Slack import.', description: error })
  }

  async function handleRun() {
    const { ok, error } = await runSync()
    if (ok) {
      showSnackbar({ level: 'success', text: 'Slack import finished.' })
      onSynced?.()
    } else {
      showSnackbar({ level: 'error', text: 'The Slack import failed.', description: error })
    }
  }

  async function handleRemove() {
    const { ok, error } = await deleteSync()
    if (ok) showSnackbar({ level: 'success', text: 'Slack import removed. Users and groups stay.' })
    else showSnackbar({ level: 'error', text: 'Failed to remove the Slack import.', description: error })
  }

  return (
    <SectionRow
      title="Slack user groups"
      badge={<Badge variant={enabled ? 'active' : 'inactive'}>{enabled ? 'On' : 'Off'}</Badge>}
      description="Members of these groups become users, in a group named after the handle."
      callout={<DocsBtnCallOut href={docsUrl.controlPlane.slack} text="How the Slack import works" />}
    >
      <Stack gap="md">
        <MultiSelect
          label="User groups"
          placeholder={groupsStatus === 'loading' ? 'Loading user groups…' : 'Select user groups'}
          data={groupOptions}
          value={groupIds}
          onChange={setGroupIds}
          error={groupsErrorText(groupsError)}
          searchable
        />
        <NumberInput
          label="Sync every (minutes)"
          min={5}
          w={200}
          value={intervalMinutes}
          onChange={setIntervalMinutes}
        />
        {enabled && <LastRun sync={sync} />}
        <Group justify="space-between" gap="sm">
          <Group gap="sm">
            {enabled && (
              <Button variant="default" onClick={handleRun} loading={running} disabled={!sync.group_ids?.length}>
                Sync now
              </Button>
            )}
          </Group>
          <Group gap="sm">
            {groupsError && (
              <Button variant="default" onClick={loadGroups}>
                Reload groups
              </Button>
            )}
            {enabled && (
              <Button variant="subtle" color="red" onClick={handleRemove} loading={saving}>
                Remove
              </Button>
            )}
            <Button onClick={handleSave} loading={saving}>
              Save
            </Button>
          </Group>
        </Group>
      </Stack>
    </SectionRow>
  )
}

export default function SlackImportTab({ onSynced }) {
  const status = useSlackImportStore((s) => s.status)
  const sync = useSlackImportStore((s) => s.sync)
  const load = useSlackImportStore((s) => s.load)

  useEffect(() => {
    load()
  }, [load])

  const showLoader = useMinDelay(status === 'loading' || status === 'idle')
  if (showLoader) return <PageLoader h={240} />
  if (status === 'error') return <PageLoader error h={240} message="Failed to load the Slack import." />

  return <SlackImportForm key={sync?.enabled ? 'on' : 'off'} sync={sync} onSynced={onSynced} />
}
