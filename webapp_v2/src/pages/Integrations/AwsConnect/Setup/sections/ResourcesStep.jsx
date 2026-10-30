import { useState } from 'react'
import { Group, Stack, Text, Title } from '@mantine/core'
import { ChevronDown, ChevronUp, Info } from 'lucide-react'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import Table from '@/components/Table'
import TextInput from '@/components/TextInput'
import { useUserStore } from '@/stores/useUserStore'
import TreeTable from '../../components/TreeTable'
import { useAwsConnectSetupStore } from '../store'
import { accountChecked, resourceIds, toggleResource } from '../wizard'

const STATUS_COLOR = {
  available: 'green',
  active: 'green',
  'backing-up': 'indigo',
  'configuring-enhanced-monitoring': 'indigo',
  'configuring-iam-database-auth': 'indigo',
  'configuring-log-exports': 'indigo',
  'converting-to-vpc': 'indigo',
  creating: 'indigo',
  maintenance: 'indigo',
  modifying: 'indigo',
  'moving-to-vpc': 'indigo',
  rebooting: 'indigo',
  renaming: 'indigo',
  'resetting-master-credentials': 'indigo',
  starting: 'indigo',
  'storage-optimization': 'indigo',
  upgrading: 'indigo',
  stopped: 'amber',
  stopping: 'amber',
  'storage-full': 'amber',
  deleting: 'red',
  failed: 'red',
  inactive: 'red',
  suspended: 'red',
  'inaccessible-encryption-credentials': 'red',
  'incompatible-network': 'red',
  'incompatible-option-group': 'red',
  'incompatible-parameters': 'red',
  'incompatible-restore': 'red',
  'restore-error': 'red',
}

function ResourceStatusBadge({ status }) {
  const value = (status ?? '').toLowerCase()
  return (
    <Badge fullLabel variant="light" color={STATUS_COLOR[value] ?? 'gray'} tt="none">
      {value}
    </Badge>
  )
}

const FREE_MESSAGE =
  'Organizations with Free plan can import only one resource. Upgrade to Enterprise to have unlimited access to Resource Discovery.'

function SkippedResources({ connected }) {
  const [shown, setShown] = useState(false)
  return (
    <Stack gap="md" w="100%" align="center">
      <Stack gap="xs" maw={600} w="100%" align="flex-start">
        <Title order={3}>Skipped Resources</Title>
        <Text size="sm" c="dimmed">
          Additional resources found in already created connections.
        </Text>
        <Button
          variant="light"
          rightSection={shown ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
          aria-expanded={shown}
          onClick={() => setShown((value) => !value)}
        >
          {shown ? 'Hide' : 'Show'}
        </Button>
      </Stack>
      {shown && (
        <Table>
          <Table.Thead>
            <Table.Tr>
              <Table.Th w="50%">Resources</Table.Th>
              <Table.Th w="50%">Status</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {connected.map((instance) => (
              <Table.Tr key={instance.arn}>
                <Table.Td>{instance.name}</Table.Td>
                <Table.Td>
                  <ResourceStatusBadge status={instance.status} />
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}
    </Stack>
  )
}

export default function ResourcesStep() {
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const tree = useAwsConnectSetupStore((s) => s.tree)
  const connected = useAwsConnectSetupStore((s) => s.connected)
  const skipConnected = useAwsConnectSetupStore((s) => s.skipConnected)
  const selected = useAwsConnectSetupStore((s) => s.selectedResources)
  const securityGroups = useAwsConnectSetupStore((s) => s.securityGroups)
  const update = useAwsConnectSetupStore((s) => s.update)
  const setMapValue = useAwsConnectSetupStore((s) => s.setMapValue)
  const [expanded, setExpanded] = useState([])

  const expand = (id) => setExpanded((ids) => (ids.includes(id) ? ids : [...ids, id]))
  const allIds = resourceIds(tree)
  const all = selected.length === 0 ? false : selected.length === allIds.length ? true : 'some'

  const applyToAccount = (accountId, cidr) => {
    const account = tree.find((row) => row.id === accountId)
    for (const child of account?.children ?? []) setMapValue('securityGroups', child.id, cidr)
  }

  const columns = [
    { id: 'name', header: 'Name', width: '30%' },
    { id: 'account', header: 'Account ID', width: '15%', render: (row) => (row.isAccount ? row.id : '') },
    {
      id: 'status',
      header: 'Status',
      width: '10%',
      render: (row) => <ResourceStatusBadge status={row.status} />,
    },
    {
      id: 'securityGroup',
      header: 'Security Group',
      width: '25%',
      render: (row) => {
        if (row.isAccount) return ''
        const cidr = securityGroups[row.id] ?? ''
        return (
          <Group gap="xs" wrap="nowrap">
            <TextInput
              flex={1}
              aria-label={`Security group CIDR for ${row.name}`}
              placeholder="e.g. 10.10.10.10/32"
              value={cidr}
              onChange={(e) => setMapValue('securityGroups', row.id, e.currentTarget.value)}
            />
            <Button
              size="sm"
              variant="light"
              flex="none"
              disabled={!cidr}
              onClick={() => applyToAccount(row.accountId, cidr)}
            >
              Apply to all
            </Button>
          </Group>
        )
      },
    },
  ]

  return (
    <Stack gap="xl" w="100%" align="center">
      <Stack gap="md" maw={600} w="100%">
        <Stack gap="xs">
          <Title order={3}>AWS Resources</Title>
          <Text size="sm" c="dimmed">
            Select the specific AWS resources you wish to connect. You may choose multiple resources across your
            accounts to enable full functionality.
          </Text>
        </Stack>
        {isFreeLicense && <FreeLicenseCallout message={FREE_MESSAGE} />}
        <Alert color="indigo" variant="light" icon={<Info size={16} />}>
          During this Beta release, our service currently supports database resources only.
        </Alert>
      </Stack>

      <TreeTable
        columns={columns}
        rows={tree}
        empty="No AWS resources found"
        expanded={expanded}
        onToggleExpand={(id) => setExpanded((ids) => (ids.includes(id) ? ids.filter((x) => x !== id) : [...ids, id]))}
        selection={{
          checked: (row) => (row.isAccount ? accountChecked(row, selected) : selected.includes(row.id)),
          selectable: (row) => !row.error && !(isFreeLicense && row.isAccount),
          onChange: (row, checked) => {
            update({ selectedResources: toggleResource(selected, row, checked, isFreeLicense) })
            if (checked) expand(row.isAccount ? row.id : row.accountId)
          },
          all,
          allDisabled: isFreeLicense,
          onChangeAll: (checked) => {
            update({ selectedResources: checked ? allIds : [] })
            setExpanded(checked ? tree.map((row) => row.id) : [])
          },
        }}
      />

      {skipConnected && connected.length > 0 && <SkippedResources connected={connected} />}
    </Stack>
  )
}
