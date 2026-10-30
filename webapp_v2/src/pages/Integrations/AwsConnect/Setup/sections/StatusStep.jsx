import { Group, Loader, Stack, Text, Title } from '@mantine/core'
import { Check, X } from 'lucide-react'
import Badge from '@/components/Badge'
import TreeTable from '../../components/TreeTable'
import { useAwsConnectSetupStore } from '../store'
import { statusRows } from '../wizard'

const STATUS = {
  pending: { color: 'indigo', label: 'Starting...', icon: <Loader size="xs" /> },
  success: { color: 'green', label: 'Started', icon: <Check size={18} color="var(--mantine-color-green-6)" aria-hidden /> },
  failure: { color: 'red', label: 'Failed', icon: <X size={18} color="var(--mantine-color-red-6)" aria-hidden /> },
}

function JobStatus({ status }) {
  const known = STATUS[status]
  return (
    <Group gap="xs" wrap="nowrap">
      {known?.icon}
      <Badge fullLabel variant="light" color={known?.color ?? 'gray'}>
        {known?.label ?? status}
      </Badge>
    </Group>
  )
}

const COLUMNS = [
  { id: 'name', header: 'Resource Roles Name', width: '40%' },
  { id: 'engine', header: 'Type', width: '20%' },
  { id: 'status', header: 'Status', width: '20%', render: (row) => <JobStatus status={row.status} /> },
]

export default function StatusStep() {
  const jobs = useAwsConnectSetupStore((s) => s.jobs)

  return (
    <Stack gap="xl" w="100%" align="center">
      <Stack gap="xs" maw={600} w="100%">
        <Title order={3}>Creating Resource Roles</Title>
        <Text size="sm" c="dimmed">
          Hoop starts one job for each resource. The jobs run in the background and can take a few minutes. Go to AWS
          Connect to see when each job completes.
        </Text>
      </Stack>
      <TreeTable columns={COLUMNS} rows={statusRows(jobs)} empty="No connections are being created" />
    </Stack>
  )
}
