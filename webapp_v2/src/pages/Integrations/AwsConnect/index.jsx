import { useEffect, useMemo } from 'react'
import { useNavigate } from 'react-router-dom'
import { Group, Loader, Paper, Stack, Text, Title } from '@mantine/core'
import { Cloud, RefreshCw } from 'lucide-react'
import Alert from '@/components/Alert'
import Badge from '@/components/Badge'
import Button from '@/components/Button'
import FreeLicenseCallout from '@/components/FreeLicenseCallout'
import PageLoader from '@/components/PageLoader'
import { useUserStore } from '@/stores/useUserStore'
import TreeTable from './components/TreeTable'
import { useAwsJobsStore } from './store'
import { formatDate, isRunning, limitReached, SETUP_PATH, toJobRows } from './utils'

const LIMIT_MESSAGE =
  'Your organization has reached Resource Discovery free usage limits. Upgrade to Enterprise to have unlimited access to Resource Discovery.'
const INFO_MESSAGE =
  'Organizations with Free plan can import only one resource. Upgrade to Enterprise to have unlimited access to Resource Discovery.'

const JOB_STATUS = {
  running: { color: 'amber', label: 'In Progress' },
  completed: { color: 'green', label: 'Completed' },
  failed: { color: 'red', label: 'Failed' },
  error: { color: 'red', label: 'Error' },
}

function JobStatusBadge({ status }) {
  const known = JOB_STATUS[status]
  return (
    <Badge fullLabel variant="light" color={known?.color ?? 'gray'}>
      {known?.label ?? status}
    </Badge>
  )
}

const COLUMNS = [
  { id: 'name', header: 'Resource', width: '35%' },
  { id: 'status', header: 'Status', width: '15%', render: (row) => <JobStatusBadge status={row.status} /> },
  { id: 'createdAt', header: 'Created At', width: '25%', render: (row) => formatDate(row.createdAt) },
  { id: 'completedAt', header: 'Completed At', width: '25%', render: (row) => formatDate(row.completedAt) },
]

export default function AwsConnect() {
  const navigate = useNavigate()
  const isFreeLicense = useUserStore((s) => s.isFreeLicense)
  const jobs = useAwsJobsStore((s) => s.jobs)
  const loaded = useAwsJobsStore((s) => s.loaded)
  const error = useAwsJobsStore((s) => s.error)
  const fetchJobs = useAwsJobsStore((s) => s.fetchJobs)
  const startPolling = useAwsJobsStore((s) => s.startPolling)
  const stopPolling = useAwsJobsStore((s) => s.stopPolling)

  const running = jobs.some(isRunning)
  const limit = isFreeLicense && limitReached(jobs)
  const rows = useMemo(() => toJobRows(jobs), [jobs])

  useEffect(() => {
    startPolling()
    return stopPolling
  }, [startPolling, stopPolling])

  return (
    <Stack gap="xl">
      <Title order={1}>AWS Connect</Title>

      {isFreeLicense && (
        <FreeLicenseCallout variant={limit ? 'limit' : 'info'} message={limit ? LIMIT_MESSAGE : INFO_MESSAGE} />
      )}

      <Paper withBorder p="xl">
        <Stack gap="md" align="flex-start">
          <Group gap="xs">
            <Cloud size={24} color="var(--mantine-color-indigo-6)" aria-hidden />
            <Title order={2} size="h3">
              Automatic AWS Database Discovery
            </Title>
          </Group>
          <Text size="sm">
            Connect to your AWS environment to automatically discover and configure your database resources. This
            automated process will scan your AWS account for database instances and create resource roles in Hoop,
            saving you time and ensuring proper configuration.
          </Text>
          <Button disabled={limit} onClick={() => navigate(SETUP_PATH)}>
            Discover AWS Databases
          </Button>
        </Stack>
      </Paper>

      <Stack gap="sm">
        <Group justify="space-between">
          <Title order={2} size="h4">
            Resource Role Creation Processes
          </Title>
          <Group gap="md">
            {running && (
              <Group gap="xs" role="status">
                <Loader size="xs" />
                <Text size="sm" c="indigo">
                  Resource role creation in progress
                </Text>
              </Group>
            )}
            <Button size="sm" variant="light" leftSection={<RefreshCw size={14} />} onClick={fetchJobs}>
              Refresh
            </Button>
          </Group>
        </Group>

        {error && (
          <Alert color="red" variant="light">
            {error}
          </Alert>
        )}

        {loaded ? (
          <TreeTable
            columns={COLUMNS}
            rows={rows}
            empty="No database discovery processes found. Start a new AWS connection to automatically discover and configure your database resources."
          />
        ) : (
          <PageLoader h={240} />
        )}
      </Stack>
    </Stack>
  )
}
