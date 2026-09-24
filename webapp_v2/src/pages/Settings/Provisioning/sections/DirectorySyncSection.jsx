import { useState } from 'react'
import { Group, Stack, Text } from '@mantine/core'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import MultiSelect from '@/components/MultiSelect'
import NumberInput from '@/components/NumberInput'
import PasswordInput from '@/components/PasswordInput'
import SectionRow from '@/components/SectionRow'
import Select from '@/components/Select'
import Textarea from '@/components/Textarea'
import TextInput from '@/components/TextInput'
import { showSnackbar } from '@/utils/snackbar'
import { useProvisioningStore } from '../store'

const PROVIDERS = [
  { value: 'google', label: 'Google Workspace' },
  { value: 'auth0', label: 'Auth0' },
  { value: 'cognito', label: 'AWS Cognito' },
]

// The fields each provider needs; `secret` renders a password input and comes
// back from the API as "********", which keeps the stored value when sent.
const FIELDS = {
  google: [
    { key: 'service_account_json', label: 'Service account key (JSON)', secret: true, multiline: true, required: true },
    { key: 'admin_email', label: 'Admin email to act as', required: true, placeholder: 'admin@example.com' },
    { key: 'customer', label: 'Customer ID', placeholder: 'my_customer' },
  ],
  auth0: [
    { key: 'domain', label: 'Domain', required: true, placeholder: 'example.us.auth0.com' },
    { key: 'client_id', label: 'Client ID', required: true },
    { key: 'client_secret', label: 'Client secret', secret: true, required: true },
  ],
  cognito: [
    { key: 'region', label: 'Region', required: true, placeholder: 'us-east-1' },
    { key: 'user_pool_id', label: 'User pool ID', required: true, placeholder: 'us-east-1_AbCdEf123' },
    { key: 'access_key_id', label: 'Access key ID', placeholder: 'Empty uses the control plane AWS role' },
    { key: 'secret_access_key', label: 'Secret access key', secret: true },
  ],
}

const HINTS = {
  google: 'The service account needs domain-wide delegation with the admin.directory group, group.member and user readonly scopes. Groups are named by their email.',
  auth0: 'A machine-to-machine application authorized on the Management API with read:users and read:roles. Roles are the groups.',
  cognito: 'Credentials need cognito-idp:ListGroups and cognito-idp:ListUsersInGroup on the user pool.',
}

function formatDate(value) {
  return value ? new Date(value).toLocaleString() : 'never'
}

// Directory sync: the control plane pulls the members of the chosen groups.
export default function DirectorySyncSection({ disabled }) {
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

  const [provider, setProvider] = useState(sync?.provider || 'google')
  const [settings, setSettings] = useState(sync?.settings ?? {})
  const [groupIds, setGroupIds] = useState(sync?.group_ids ?? [])
  const [intervalMinutes, setIntervalMinutes] = useState(sync?.interval_minutes ?? 15)

  const savedProvider = sync?.enabled ? sync.provider : null
  const groupOptions = [
    ...groups.map((g) => ({ value: g.id, label: g.name })),
    // Keep a selected id visible before the list is loaded.
    ...groupIds.filter((id) => !groups.some((g) => g.id === id)).map((id) => ({ value: id, label: id })),
  ]

  function changeProvider(value) {
    setProvider(value)
    setSettings(value === savedProvider ? (sync?.settings ?? {}) : {})
    setGroupIds(value === savedProvider ? (sync?.group_ids ?? []) : [])
  }

  async function handleSave() {
    const { ok, error } = await saveSync({
      provider,
      settings,
      group_ids: groupIds,
      interval_minutes: Number(intervalMinutes) || 15,
    })
    if (ok) showSnackbar({ level: 'success', text: 'Directory sync saved.' })
    else showSnackbar({ level: 'error', text: 'Failed to save the directory sync.', description: error })
  }

  async function handleRun() {
    const { ok, error } = await runSync()
    if (ok) showSnackbar({ level: 'success', text: 'Directory sync finished.' })
    else showSnackbar({ level: 'error', text: 'Directory sync failed.', description: error })
  }

  async function handleDelete() {
    const { ok, error } = await deleteSync()
    if (ok) {
      setSettings({})
      setGroupIds([])
      showSnackbar({ level: 'success', text: 'Directory sync removed.' })
    } else {
      showSnackbar({ level: 'error', text: 'Failed to remove the directory sync.', description: error })
    }
  }

  return (
    <Stack gap="xl">
      <SectionRow
        title="Identity provider"
        badge={
          <Badge variant={sync?.enabled ? 'active' : 'inactive'}>{sync?.enabled ? 'Enabled' : 'Disabled'}</Badge>
        }
        description={HINTS[provider]}
      >
        <Stack gap="md">
          <Select label="Provider" data={PROVIDERS} value={provider} onChange={changeProvider} allowDeselect={false} />
          {FIELDS[provider].map((field) => {
            const key = `${provider}-${field.key}`
            const common = {
              label: field.label,
              placeholder: field.placeholder,
              required: field.required,
              value: settings[field.key] ?? '',
              onChange: (e) => setSettings((s) => ({ ...s, [field.key]: e.currentTarget.value })),
            }
            if (field.multiline && settings[field.key] !== '********') return <Textarea key={key} {...common} minRows={3} />
            if (field.secret) return <PasswordInput key={key} {...common} />
            return <TextInput key={key} {...common} />
          })}
        </Stack>
      </SectionRow>

      <SectionRow
        title="Groups to sync"
        description="Only members of these groups become Hoop users. Name them as reviewers on the analyzer rules."
      >
        <Stack gap="md">
          <MultiSelect
            label="Groups"
            data={groupOptions}
            value={groupIds}
            onChange={setGroupIds}
            searchable
            placeholder={savedProvider === provider ? 'Load the groups of the provider' : 'Save the credentials first'}
          />
          {groupsStatus === 'error' && (
            <Text size="sm" c="red">
              {groupsError}
            </Text>
          )}
          <Group justify="flex-start">
            <Button
              variant="outline"
              size="xs"
              onClick={loadGroups}
              loading={groupsStatus === 'loading'}
              disabled={savedProvider !== provider}
            >
              Load groups
            </Button>
          </Group>
          <NumberInput label="Minutes between runs" min={5} value={intervalMinutes} onChange={setIntervalMinutes} />
        </Stack>
      </SectionRow>

      {sync?.enabled && (
        <SectionRow title="Last run" description="The sync runs on its interval and on demand.">
          <Stack gap="xs">
            <Text size="sm">{`Last run: ${formatDate(sync.last_run_at)}`}</Text>
            {sync.last_error ? (
              <Text size="sm" c="red">
                {sync.last_error}
              </Text>
            ) : (
              sync.last_run_at && (
                <Text size="sm" c="dimmed">
                  Succeeded.
                </Text>
              )
            )}
          </Stack>
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
        <Button onClick={handleSave} loading={saving} disabled={disabled}>
          Save
        </Button>
      </Group>
    </Stack>
  )
}
