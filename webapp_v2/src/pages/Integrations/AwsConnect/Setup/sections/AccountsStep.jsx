import { Stack, Text, Title } from '@mantine/core'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import Switch from '@/components/Switch'
import TreeTable from '../../components/TreeTable'
import { useAwsConnectSetupStore } from '../store'

const ACCOUNT_STATUS_COLOR = { ACTIVE: 'green', SUSPENDED: 'red' }

const COLUMNS = [
  { id: 'name', header: 'Account Name', width: '40%' },
  { id: 'id', header: 'Account ID', width: '30%' },
  {
    id: 'status',
    header: 'Status',
    width: '30%',
    render: (row) => (
      <Badge fullLabel variant="light" color={ACCOUNT_STATUS_COLOR[row.status] ?? 'gray'}>
        {row.status}
      </Badge>
    ),
  },
]

export default function AccountsStep() {
  const accounts = useAwsConnectSetupStore((s) => s.accounts)
  const selected = useAwsConnectSetupStore((s) => s.selectedAccounts)
  const skipConnected = useAwsConnectSetupStore((s) => s.skipConnected)
  const error = useAwsConnectSetupStore((s) => s.accountsError)
  const update = useAwsConnectSetupStore((s) => s.update)

  const rows = accounts.map((account) => ({ id: account.account_id, name: account.name, status: account.status }))
  const all = selected.length === 0 ? false : selected.length === rows.length ? true : 'some'

  return (
    <Stack gap="xl" w="100%" align="center">
      <Stack gap="xs" maw={600} w="100%">
        <Title order={3}>AWS Accounts</Title>
        <Text size="sm" c="dimmed">
          Select the AWS accounts you want to scan for database resources. Only accounts with database resources will
          be displayed in the next step.
        </Text>
      </Stack>

      {error && (
        <Alert color="red" variant="light" role="alert" maw={600} w="100%">
          {error}
        </Alert>
      )}

      <TreeTable
        columns={COLUMNS}
        rows={rows}
        empty="No AWS accounts found. Please check your credentials and try again."
        selection={{
          checked: (row) => selected.includes(row.id),
          selectable: () => true,
          onChange: (row, checked) =>
            update({ selectedAccounts: checked ? [...selected, row.id] : selected.filter((id) => id !== row.id) }),
          all,
          onChangeAll: (checked) => update({ selectedAccounts: checked ? rows.map((row) => row.id) : [] }),
        }}
      />

      <Stack gap="md" maw={600} w="100%">
        <Title order={3}>Additional Configuration</Title>
        <Switch
          checked={skipConnected}
          onChange={(e) => update({ skipConnected: e.currentTarget.checked })}
          label="Skip Connected Resources"
          description="Automatically ignore resources already used in existing resource roles."
        />
      </Stack>
    </Stack>
  )
}
